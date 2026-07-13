package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/gcsfetch"
	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/spf13/cobra"
)

var (
	transcribeLanguage  string
	transcribeArch      string
	transcribeProviders string
	transcribeWithAudio bool
	transcribeOutput    string
)

var transcribeCmd = &cobra.Command{
	Use:     "transcribe <file|gs://bucket/object>",
	GroupID: "voice",
	Short:   "Transcribe a local audio file or a GCS object to text",
	Args:    cobra.ExactArgs(1),
	Long: `Transcribes a single audio file, either from the local filesystem or a
gs:// Google Cloud Storage URI (downloaded first via application default
credentials). Currently supports .wav input directly; other formats need
converting to WAV first (e.g. with ffmpeg).

Prints load/decode/inference timing stats to stderr; use --json for a single
machine-readable result on stdout.`,
	RunE: runTranscribe,
}

func init() {
	transcribeCmd.Flags().StringVar(&transcribeLanguage, "language", "en", "STT model language (must match the language passed to 'moonshine setup')")
	transcribeCmd.Flags().StringVar(&transcribeArch, "arch", "tiny", "Model architecture (see 'moonshine setup --help')")
	transcribeCmd.Flags().StringVar(&transcribeProviders, "providers", defaultOrtProviders(), "Comma-separated ONNX Runtime execution providers, e.g. 'CoreML,CPU' on macOS (default: CPU-only; see docs/hardware-acceleration.md before enabling CoreML)")
	transcribeCmd.Flags().BoolVar(&transcribeWithAudio, "with-audio", false, "Include each line's raw per-line audio samples in --json output")
	transcribeCmd.Flags().StringVarP(&transcribeOutput, "output", "o", "", "Also write the transcript to this file (plain text, or JSON if --json is set), in addition to stdout")
}

type transcribeStats struct {
	ModelLoadMs      float64 `json:"model_load_ms"`
	DownloadMs       float64 `json:"download_ms,omitempty"`
	DecodeMs         float64 `json:"decode_ms"`
	InferenceMs      float64 `json:"inference_ms"`
	AudioDurationSec float64 `json:"audio_duration_sec"`
	RealTimeFactor   float64 `json:"real_time_factor"`
}

type transcribeResult struct {
	Lines []moonshine.Line `json:"lines"`
	Stats transcribeStats  `json:"stats"`
}

func runTranscribe(cmd *cobra.Command, args []string) error {
	if err := loadLibrary(); err != nil {
		return err
	}
	arch, err := modelArchFromFlag(transcribeArch)
	if err != nil {
		return err
	}

	input := args[0]
	var stats transcribeStats

	localPath := input
	if gcsfetch.IsGCSURI(input) {
		tmpDir, err := os.MkdirTemp("", "moonshine-gcs-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)

		t0 := time.Now()
		err = withProgress(fmt.Sprintf("downloading %s", input), func() error {
			var derr error
			localPath, derr = gcsfetch.Download(context.Background(), input, tmpDir)
			return derr
		})
		if err != nil {
			return err
		}
		stats.DownloadMs = msSince(t0)
	}

	var samples []float32
	t0 := time.Now()
	if err := withProgress("decoding audio", func() error {
		var derr error
		samples, derr = audio.LoadFile(localPath)
		return derr
	}); err != nil {
		return err
	}
	stats.DecodeMs = msSince(t0)
	stats.AudioDurationSec = float64(len(samples)) / float64(audio.TargetSampleRate)

	var tr *moonshine.Transcriber
	t0 = time.Now()
	if err := withProgress(fmt.Sprintf("loading %s model", transcribeArch), func() error {
		var derr error
		tr, derr = loadTranscriberFor(transcribeLanguage, arch, ortProviderOptions(transcribeProviders)...)
		return derr
	}); err != nil {
		return err
	}
	defer tr.Close()
	stats.ModelLoadMs = msSince(t0)

	var transcript moonshine.Transcript
	t0 = time.Now()
	if err := withProgress(fmt.Sprintf("transcribing %.1fs of audio", stats.AudioDurationSec), func() error {
		var derr error
		transcript, derr = tr.Transcribe(samples, audio.TargetSampleRate, 0)
		return derr
	}); err != nil {
		return err
	}
	stats.InferenceMs = msSince(t0)
	if stats.InferenceMs > 0 {
		stats.RealTimeFactor = stats.AudioDurationSec / (stats.InferenceMs / 1000.0)
	}

	lines := transcript.Lines
	if !transcribeWithAudio {
		lines = make([]moonshine.Line, len(transcript.Lines))
		copy(lines, transcript.Lines)
		for i := range lines {
			lines[i].AudioData = nil
		}
	}

	if transcribeOutput != "" {
		if err := writeTranscribeOutput(transcribeOutput, lines, stats, jsonOutput()); err != nil {
			return fmt.Errorf("writing --output file: %w", err)
		}
	}

	if jsonOutput() {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(transcribeResult{Lines: lines, Stats: stats})
	}

	for _, line := range transcript.Lines {
		fmt.Printf("%s %s\n", styleID.Render(fmt.Sprintf("[%6.2fs]", line.StartTime)), line.Text)
	}
	fmt.Fprintln(os.Stderr, separator())
	fmt.Fprintf(os.Stderr, "%s load=%.0fms decode=%.0fms infer=%.0fms audio=%.2fs rtf=%.1fx\n",
		muted("stats:"), stats.ModelLoadMs, stats.DecodeMs, stats.InferenceMs, stats.AudioDurationSec, stats.RealTimeFactor)
	if transcribeOutput != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", muted("saved:"), transcribeOutput)
	}
	return nil
}

// writeTranscribeOutput writes lines/stats to path: plain "[start] text"
// lines by default, or the same JSON shape as --json's stdout output when
// asJSON is true.
func writeTranscribeOutput(path string, lines []moonshine.Line, stats transcribeStats, asJSON bool) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if asJSON {
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		return enc.Encode(transcribeResult{Lines: lines, Stats: stats})
	}
	for _, l := range lines {
		if _, err := fmt.Fprintf(f, "[%6.2fs] %s\n", l.StartTime, l.Text); err != nil {
			return err
		}
	}
	return nil
}

func msSince(t0 time.Time) float64 {
	return float64(time.Since(t0).Microseconds()) / 1000.0
}

func separator() string {
	return styleMuted.Render("--------------------------------------------------")
}
