package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/session"
	"github.com/ghchinoy/moonshine-go/internal/tui"
)

var (
	liveLanguage     string
	liveArch         string
	liveProviders    string
	liveNoTUI        bool
	livePollInterval time.Duration
	liveOutput       string
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
	liveCmd.Flags().StringVar(&liveArch, "arch", "tiny-streaming", "Model architecture (see 'moonshine setup --help'; use a *-streaming arch for best live latency; not shared with transcribe's stt.arch, since streaming archs need a different default)")
	liveCmd.Flags().StringVar(&liveProviders, "providers", defaultOrtProviders(), "Comma-separated ONNX Runtime execution providers, e.g. 'CoreML,CPU' on macOS (default: CPU-only; see docs/hardware-acceleration.md before enabling CoreML)")
	liveCmd.Flags().BoolVar(&liveNoTUI, "no-tui", false, "Print plain text updates instead of the bubbletea UI (for scripting/logging)")
	liveCmd.Flags().DurationVar(&livePollInterval, "poll-interval", 250*time.Millisecond, "How often to poll for updated transcripts")
	liveCmd.Flags().StringVarP(&liveOutput, "output", "o", "", "Also append completed lines to this file as they finalize, in addition to the TUI/stdout")
}

func runLive(cmd *cobra.Command, args []string) error {
	if err := loadLibrary(); err != nil {
		return err
	}
	arch, err := modelArchFromFlag(liveArch)
	if err != nil {
		return err
	}
	language := flagOrConfig(cmd, "language", "stt.language", liveLanguage)

	if !jsonOutput() {
		fmt.Fprintln(os.Stderr, muted("loading model..."))
	}
	tr, err := loadTranscriberFor(language, arch, ortProviderOptions(liveProviders)...)
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

	sess, err := session.NewLive(tr, mic, livePollInterval)
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

	if liveNoTUI {
		return runLivePlain(updates)
	}
	p := tea.NewProgram(tui.NewLive(updates, cancel))
	_, err = p.Run()
	return err
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
					fmt.Fprintf(f, "[%6.2fs] %s\n", l.StartTime, l.Text)
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
			fmt.Println(l.Text)
		}
		if u.Done {
			ttft := "-"
			if u.TTFT > 0 {
				ttft = u.TTFT.String()
			}
			fmt.Fprintf(os.Stderr, "%s ttft=%s elapsed=%s\n", muted("stats:"), ttft, u.Elapsed)
		}
	}
	return nil
}
