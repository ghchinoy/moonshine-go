package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/session"
	"github.com/ghchinoy/moonshine-go/internal/tui"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

var (
	liveLanguage                    string
	liveArch                        string
	liveProviders                   string
	liveNoTUI                       bool
	livePollInterval                time.Duration
	liveOutput                      string
	liveIdentifySpeakers            bool
	liveWordTimestamps              bool
	liveDiarizationClusterCadence   float64
	liveDiarizationAnalyzeCadence   float64
	liveDiarizationClusterWindowSec float64
	liveLineStats                   bool
	liveRecordAudio                 string
	liveSaveLineAudio               string
	liveKeyterms                    string
	liveKeytermBoost                float64
	liveContext                     string
	liveContextFile                 string
)

var liveCmd = &cobra.Command{
	Use:     "live",
	GroupID: "voice",
	Short:   "Transcribe live from the microphone",
	Long: `Opens the default microphone, streams audio into moonshine's streaming
transcription API, and shows interim + final lines as they're produced,
along with time-to-first-token (TTFT) and per-poll latency stats.

Requires a build with cgo enabled (microphone capture uses
github.com/gen2brain/malgo); the libmoonshine bindings themselves do not.`,
	RunE: runLive,
}

func init() {
	liveCmd.Flags().StringVar(&liveLanguage, "language", "en", "STT model language (must match the language passed to 'moonshine setup'; config key: stt.language)")
	liveCmd.Flags().StringVar(&liveArch, "arch", "tiny-streaming", "Model architecture (see 'moonshine setup --help'; use a *-streaming arch for best live latency; config key: live.arch, not shared with transcribe's stt.arch since streaming archs need a different default)")
	liveCmd.Flags().StringVar(&liveProviders, "providers", defaultOrtProviders(), "Comma-separated ONNX Runtime execution providers, e.g. 'CoreML,CPU' on macOS (default: CPU-only; see docs/hardware-acceleration.md before enabling CoreML)")
	liveCmd.Flags().BoolVar(&liveNoTUI, "no-tui", false, "Print plain text updates instead of the bubbletea UI (for scripting/logging)")
	liveCmd.Flags().DurationVar(&livePollInterval, "poll-interval", 250*time.Millisecond, "How often to poll for updated transcripts")
	liveCmd.Flags().StringVarP(&liveOutput, "output", "o", "", "Also append completed lines to this file as they finalize, in addition to the TUI/stdout")
	liveCmd.Flags().BoolVar(&liveIdentifySpeakers, "identify-speakers", false, "Enable speaker diarization: lines get a speaker label like [S0], and --json output gets a speaker_spans array (implies --word-timestamps; adds significant compute, and re-clustering cost grows with session length -- see the diarization-* tuning flags)")
	liveCmd.Flags().BoolVar(&liveWordTimestamps, "word-timestamps", false, "Enable per-word timing: --json output gets a words array per line (automatically enabled by --identify-speakers)")
	liveCmd.Flags().Float64Var(&liveDiarizationClusterCadence, "diarization-cluster-cadence", 2.0, "Minimum seconds between diarization re-clustering passes; raise to reduce cost on long sessions (only applies with --identify-speakers)")
	liveCmd.Flags().Float64Var(&liveDiarizationAnalyzeCadence, "diarization-analyze-cadence", 1.0, "Seconds between diarization segmentation/embedding model runs (only applies with --identify-speakers)")
	liveCmd.Flags().Float64Var(&liveDiarizationClusterWindowSec, "diarization-cluster-window-sec", 120.0, "How much audio history diarization re-clustering considers on each refresh; 0 = unlimited full history (only applies with --identify-speakers)")
	liveCmd.Flags().BoolVar(&liveLineStats, "line-stats", false, "In --no-tui mode, print a stderr note per finalized line with its time-to-final and revision count (the bubbletea TUI always shows this in its footer; the end-of-session summary is always printed either way)")
	liveCmd.Flags().StringVar(&liveRecordAudio, "record-audio", "", "Record captured microphone audio to a WAV file (pass filename or directory; default filename: moonshine_clip_YYYYMMDD-HHMMSS_16k_mono.wav)")
	liveCmd.Flags().StringVar(&liveSaveLineAudio, "save-line-audio", "", "Save each finalized line's raw audio and transcript text as individual .wav and .txt files under this directory")
	liveCmd.Flags().StringVar(&liveKeyterms, "keyterms", "", "Comma-separated key terms to bias speech recognition towards (e.g. 'Kubernetes,Ceph,etcd'; streaming models only)")
	liveCmd.Flags().Float64Var(&liveKeytermBoost, "keyterm-boost", 2.0, "Keyterm biasing boost strength (default: 2.0; streaming models only)")
	liveCmd.Flags().StringVar(&liveContext, "context", "", "Passage of free-form text from which key terms are automatically extracted (streaming models only)")
	liveCmd.Flags().StringVar(&liveContextFile, "context-file", "", "Path to file containing free-form context text for keyterm extraction (streaming models only)")
}

func runLive(cmd *cobra.Command, args []string) error {
	if err := loadLibrary(); err != nil {
		return err
	}
	archFlag := flagOrConfig(cmd, "arch", "live.arch", liveArch)
	arch, err := modelArchFromFlag(archFlag)
	if err != nil {
		return err
	}
	language := flagOrConfig(cmd, "language", "stt.language", liveLanguage)

	if !jsonOutput() {
		fmt.Fprintln(os.Stderr, muted("loading model..."))
	}
	customOpts, err := domainCustomizationOptions(cmd, liveKeyterms, liveKeytermBoost, liveContext, liveContextFile)
	if err != nil {
		return err
	}
	loadOpts := append(ortProviderOptions(liveProviders),
		diarizationOptions(cmd, liveIdentifySpeakers, liveWordTimestamps,
			liveDiarizationClusterCadence, liveDiarizationAnalyzeCadence, liveDiarizationClusterWindowSec)...)
	loadOpts = append(loadOpts, customOpts...)
	tr, err := loadTranscriberFor(language, arch, loadOpts...)
	if err != nil {
		return err
	}
	defer tr.Close()

	if !jsonOutput() {
		fmt.Fprintln(os.Stderr, muted("opening microphone..."))
	}
	mic, err := audio.StartMicCapture()
	if err != nil {
		return fmt.Errorf("%w\n\n%s", err, muted("hint: check microphone permissions for this terminal in System Settings > Privacy & Security > Microphone"))
	}
	defer mic.Close()
	if !jsonOutput() {
		fmt.Fprintln(os.Stderr, muted("listening... (ctrl-c to stop)"))
	}

	var audioSource serveapi.AudioSource = mic
	var recSource *recordingAudioSource
	if liveRecordAudio != "" {
		recSource = newRecordingAudioSource(mic)
		audioSource = recSource
	}

	sess, err := session.NewLive(tr, audioSource, livePollInterval)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Also stop cleanly on Ctrl-C / SIGTERM in plain mode (the TUI handles
	// its own key input for quitting).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	go sess.Run(ctx)

	updates := sess.Updates()
	if liveSaveLineAudio != "" {
		updates = teeLineAudioToDir(updates, liveSaveLineAudio)
		if !jsonOutput() {
			fmt.Fprintf(os.Stderr, "%s %s\n", muted("saving line audio & transcripts to:"), liveSaveLineAudio)
		}
	}
	if liveOutput != "" {
		var terr error
		updates, terr = teeUpdatesToFile(updates, liveOutput)
		if terr != nil {
			return fmt.Errorf("opening --output file: %w", terr)
		}
		if !jsonOutput() {
			fmt.Fprintf(os.Stderr, "%s %s\n", muted("saving completed lines to:"), liveOutput)
		}
	}

	var runErr error
	if liveNoTUI {
		runErr = runLivePlain(updates)
	} else {
		p := tea.NewProgram(tui.NewLive(updates, cancel))
		_, runErr = p.Run()
	}

	if recSource != nil {
		path := resolveAudioRecordPath(liveRecordAudio)
		samples := recSource.RecordedSamples()
		if len(samples) > 0 {
			if sErr := audio.SaveWAV(path, samples, audio.TargetSampleRate); sErr != nil {
				fmt.Fprintf(os.Stderr, "error saving recorded audio to %s: %v\n", path, sErr)
			} else if !jsonOutput() {
				fmt.Fprintf(os.Stderr, "%s %s (%d samples, %.2fs)\n", stylePass.Render("Saved recorded audio:"), path, len(samples), float64(len(samples))/float64(audio.TargetSampleRate))
			}
		}
	}

	return runErr
}

type recordingAudioSource struct {
	source serveapi.AudioSource
	chunks chan []float32
	pcm    []float32
	mu     sync.Mutex
}

func newRecordingAudioSource(source serveapi.AudioSource) *recordingAudioSource {
	r := &recordingAudioSource{
		source: source,
		chunks: make(chan []float32, 64),
	}
	go r.run()
	return r
}

func (r *recordingAudioSource) run() {
	for chunk := range r.source.Chunks() {
		r.mu.Lock()
		r.pcm = append(r.pcm, chunk...)
		r.mu.Unlock()
		r.chunks <- chunk
	}
	close(r.chunks)
}

func (r *recordingAudioSource) Chunks() <-chan []float32 {
	return r.chunks
}

func (r *recordingAudioSource) Err() error {
	return r.source.Err()
}

func (r *recordingAudioSource) RecordedSamples() []float32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]float32, len(r.pcm))
	copy(cp, r.pcm)
	return cp
}

func teeLineAudioToDir(in <-chan session.Update, dir string) <-chan session.Update {
	out := make(chan session.Update, 8)
	go func() {
		saved := map[uint64]bool{}
		for u := range in {
			if u.Err == nil {
				for _, l := range u.Transcript.Lines {
					if !l.IsComplete || saved[l.ID] {
						continue
					}
					saved[l.ID] = true
					_ = saveLineAudio(dir, l)
				}
			}
			out <- u
		}
		close(out)
	}()
	return out
}

// teeUpdatesToFile forwards every update from in unchanged, while also
// appending newly-completed lines (deduped by line ID + text, same as
// runLivePlain) to path as "[start] text" lines as they arrive. This is
// independent of TUI vs --no-tui so both display modes get file output for
// free.
func teeUpdatesToFile(in <-chan session.Update, path string) (<-chan session.Update, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	out := make(chan session.Update, 8)
	go func() {
		printed := map[uint64]string{}
		for u := range in {
			if u.Err == nil {
				for _, l := range u.Transcript.Lines {
					if !l.IsComplete || printed[l.ID] == l.Text {
						continue
					}
					printed[l.ID] = l.Text
					prefix := fmt.Sprintf("[%6.2fs]", l.StartTime)
					if label := l.SpeakerLabel(); label != "" {
						prefix += " [" + label + "]"
					}
					fmt.Fprintf(f, "%s %s\n", prefix, l.Text)
				}
			}
			out <- u
		}
		f.Close()
		close(out)
	}()
	return out, nil
}

func runLivePlain(updates <-chan session.Update) error {
	printed := map[uint64]string{}
	for u := range updates {
		if u.Err != nil {
			fmt.Fprintln(os.Stderr, styleFail.Render("error: ")+u.Err.Error())
			continue
		}
		for _, l := range u.Transcript.Lines {
			if !l.IsComplete {
				continue
			}
			if printed[l.ID] == l.Text {
				continue
			}
			printed[l.ID] = l.Text
			if label := l.SpeakerLabel(); label != "" {
				fmt.Printf("[%s] %s\n", label, l.Text)
			} else {
				fmt.Println(l.Text)
			}
			if liveWordTimestamps {
				if words := l.WordTimingsSummary(); words != "" {
					fmt.Println(muted(words))
				}
			}
		}
		if liveLineStats {
			for _, lt := range u.FinalizedLines {
				fmt.Fprintf(os.Stderr, "%s ttf=%s revisions=%d stability=%.0f%%\n",
					muted("line-stats:"), lt.TimeToFinal, lt.Revisions, lt.StabilityRatio*100)
			}
		}
		if u.Done {
			ttft := "-"
			if u.TTFT > 0 {
				ttft = u.TTFT.String()
			}
			fmt.Fprintf(os.Stderr, "%s ttft=%s elapsed=%s\n", muted("stats:"), ttft, u.Elapsed)
			if u.Summary != nil && u.Summary.LinesFinalized > 0 {
				fmt.Fprintf(os.Stderr, "%s lines=%d avg_ttf=%s max_ttf=%s avg_revisions=%.1f avg_stability=%.0f%%\n",
					muted("summary:"), u.Summary.LinesFinalized, u.Summary.AvgTimeToFinal, u.Summary.MaxTimeToFinal,
					u.Summary.AvgRevisions, u.Summary.AvgStabilityRatio*100)
			}
		}
	}
	return nil
}
