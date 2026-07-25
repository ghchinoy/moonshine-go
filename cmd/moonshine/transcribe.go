package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/gcsfetch"
	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
	"github.com/spf13/cobra"
)

var (
	transcribeLanguage         string
	transcribeArch             string
	transcribeProviders        string
	transcribeWithAudio        bool
	transcribeOutput           string
	transcribeIdentifySpeakers bool
	transcribeWordTimestamps   bool
	transcribeConcurrency      int
	transcribeRecursive        bool
)

var transcribeCmd = &cobra.Command{
	Use:     "transcribe <file|dir|glob|gs://path>...",
	GroupID: "voice",
	Short:   "Transcribe audio files, directories, globs, or GCS URIs to text",
	Args:    cobra.MinimumNArgs(1),
	Long: `Transcribes local audio files, directories, globs, or gs:// Google Cloud
Storage URIs/prefixes. Currently supports .wav input directly; other formats
need converting to WAV first (e.g. with ffmpeg).

When given multiple inputs, transcribes them in batch mode using a worker pool
(--concurrency, default 1) and reusing a single ONNX model instance.

Prints load/decode/inference timing stats to stderr; use --json for machine-readable
results (single result for 1 file, or a full batch manifest for multiple files).`,
	RunE: runTranscribe,
}

func init() {
	transcribeCmd.Flags().StringVar(&transcribeLanguage, "language", "en", "STT model language (must match the language passed to 'moonshine setup'; config key: stt.language)")
	transcribeCmd.Flags().StringVar(&transcribeArch, "arch", "tiny", "Model architecture (see 'moonshine setup --help'; config key: stt.arch)")
	transcribeCmd.Flags().StringVar(&transcribeProviders, "providers", defaultOrtProviders(), "Comma-separated ONNX Runtime execution providers, e.g. 'CoreML,CPU' on macOS (default: CPU-only; see docs/hardware-acceleration.md before enabling CoreML)")
	transcribeCmd.Flags().BoolVar(&transcribeWithAudio, "with-audio", false, "Include each line's raw per-line audio samples in --json output")
	transcribeCmd.Flags().StringVarP(&transcribeOutput, "output", "o", "", "Also write the transcript to this file (plain text, or JSON if --json is set), in addition to stdout")
	transcribeCmd.Flags().BoolVar(&transcribeIdentifySpeakers, "identify-speakers", false, "Enable speaker diarization: --json output gets a speaker_spans array per line, and text output is prefixed with a speaker label like [S0] (implies --word-timestamps; adds significant compute)")
	transcribeCmd.Flags().BoolVar(&transcribeWordTimestamps, "word-timestamps", false, "Enable per-word timing: --json output gets a words array per line (automatically enabled by --identify-speakers)")
	transcribeCmd.Flags().IntVarP(&transcribeConcurrency, "concurrency", "c", 1, "Number of concurrent transcription workers for batch mode (default 1)")
	transcribeCmd.Flags().BoolVarP(&transcribeRecursive, "recursive", "r", true, "Recursively scan directories for audio files in batch mode")
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

type batchConfidenceSummary struct {
	Mean float64 `json:"mean"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
}

type batchSummary struct {
	TotalFiles        int                    `json:"total_files"`
	SuccessfulFiles   int                    `json:"successful_files"`
	FailedFiles       int                    `json:"failed_files"`
	TotalAudioSec     float64                `json:"total_audio_sec"`
	TotalInferenceMs  float64                `json:"total_inference_ms"`
	AggregateRTF      float64                `json:"aggregate_rtf"`
	ConfidenceSummary batchConfidenceSummary `json:"confidence_summary"`
}

type batchFileStats struct {
	DownloadMs       float64 `json:"download_ms,omitempty"`
	DecodeMs         float64 `json:"decode_ms"`
	InferenceMs      float64 `json:"inference_ms"`
	AudioDurationSec float64 `json:"audio_duration_sec"`
	RealTimeFactor   float64 `json:"real_time_factor"`
	MeanConfidence   float64 `json:"mean_confidence,omitempty"`
}

type batchFileResult struct {
	Input  string           `json:"input"`
	Status string           `json:"status"` // "ok" or "failed"
	Error  string           `json:"error,omitempty"`
	Stats  batchFileStats   `json:"stats"`
	Lines  []moonshine.Line `json:"lines,omitempty"`
}

type batchManifest struct {
	Version string            `json:"version"`
	Summary batchSummary      `json:"summary"`
	Results []batchFileResult `json:"results"`
}

func runTranscribe(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	inputs, err := expandTranscribeInputs(ctx, args, transcribeRecursive)
	if err != nil {
		return err
	}
	if len(inputs) == 0 {
		return fmt.Errorf("no input files found matching arguments")
	}

	// Single-file fast path when exactly 1 file was requested and concurrency is default
	if len(inputs) == 1 && len(args) == 1 && !gcsfetch.IsGCSURI(args[0]) && !isDir(args[0]) && transcribeConcurrency <= 1 {
		return runSingleTranscribe(cmd, inputs[0])
	}

	return runBatchTranscribe(cmd, inputs)
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func expandTranscribeInputs(ctx context.Context, args []string, recursive bool) ([]string, error) {
	var inputs []string
	for _, arg := range args {
		if gcsfetch.IsGCSURI(arg) {
			list, err := gcsfetch.ListPrefix(ctx, arg)
			if err == nil && len(list) > 0 {
				inputs = append(inputs, list...)
			} else {
				inputs = append(inputs, arg)
			}
			continue
		}

		matches, err := filepath.Glob(arg)
		if err == nil && len(matches) > 0 {
			for _, m := range matches {
				fi, err := os.Stat(m)
				if err == nil && fi.IsDir() {
					dirFiles, err := scanAudioDir(m, recursive)
					if err != nil {
						return nil, err
					}
					inputs = append(inputs, dirFiles...)
				} else if isAudioFile(m) {
					inputs = append(inputs, m)
				}
			}
			continue
		}

		fi, err := os.Stat(arg)
		if err == nil && fi.IsDir() {
			dirFiles, err := scanAudioDir(arg, recursive)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, dirFiles...)
		} else {
			inputs = append(inputs, arg)
		}
	}
	return inputs, nil
}

func scanAudioDir(dir string, recursive bool) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !recursive && info.IsDir() && path != dir {
			return filepath.SkipDir
		}
		if !info.IsDir() && isAudioFile(path) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func isAudioFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".wav"
}

func runSingleTranscribe(cmd *cobra.Command, input string) error {
	if err := loadLibrary(); err != nil {
		return err
	}
	language := flagOrConfig(cmd, "language", "stt.language", transcribeLanguage)
	archFlag := flagOrConfig(cmd, "arch", "stt.arch", transcribeArch)
	arch, err := modelArchFromFlag(archFlag)
	if err != nil {
		return err
	}

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

	loadOpts := append(ortProviderOptions(transcribeProviders),
		diarizationOptions(cmd, transcribeIdentifySpeakers, transcribeWordTimestamps, 0, 0, 0)...)

	var tr *moonshine.Transcriber
	t0 = time.Now()
	if err := withProgress(fmt.Sprintf("loading %s model", archFlag), func() error {
		var derr error
		tr, derr = loadTranscriberFor(language, arch, loadOpts...)
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
		prefix := fmt.Sprintf("[%6.2fs]", line.StartTime)
		if label := line.SpeakerLabel(); label != "" {
			prefix += " [" + label + "]"
		}
		fmt.Printf("%s %s\n", styleID.Render(prefix), line.Text)
		if transcribeWordTimestamps {
			if words := line.WordTimingsSummary(); words != "" {
				fmt.Printf("           %s\n", muted(words))
			}
		}
	}
	fmt.Fprintln(os.Stderr, separator())
	fmt.Fprintf(os.Stderr, "%s load=%.0fms decode=%.0fms infer=%.0fms audio=%.2fs rtf=%.1fx\n",
		muted("stats:"), stats.ModelLoadMs, stats.DecodeMs, stats.InferenceMs, stats.AudioDurationSec, stats.RealTimeFactor)
	if transcribeOutput != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", muted("saved:"), transcribeOutput)
	}
	return nil
}

func runBatchTranscribe(cmd *cobra.Command, inputs []string) error {
	if err := loadLibrary(); err != nil {
		return err
	}
	language := flagOrConfig(cmd, "language", "stt.language", transcribeLanguage)
	archFlag := flagOrConfig(cmd, "arch", "stt.arch", transcribeArch)
	arch, err := modelArchFromFlag(archFlag)
	if err != nil {
		return err
	}

	loadOpts := append(ortProviderOptions(transcribeProviders),
		diarizationOptions(cmd, transcribeIdentifySpeakers, transcribeWordTimestamps, 0, 0, 0)...)

	var tr *moonshine.Transcriber
	t0 := time.Now()
	if err := withProgress(fmt.Sprintf("loading %s model for batch of %d files", archFlag, len(inputs)), func() error {
		var derr error
		tr, derr = loadTranscriberFor(language, arch, loadOpts...)
		return derr
	}); err != nil {
		return err
	}
	defer tr.Close()
	modelLoadMs := msSince(t0)

	concurrency := transcribeConcurrency
	if concurrency < 1 {
		concurrency = 1
	}

	type job struct {
		index    int
		inputURI string
	}

	jobs := make(chan job, len(inputs))
	results := make([]batchFileResult, len(inputs))

	for i, input := range inputs {
		jobs <- job{index: i, inputURI: input}
	}
	close(jobs)

	var wg sync.WaitGroup
	var completedCount int32
	var mu sync.Mutex

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				res := processBatchItem(cmd, tr, j.inputURI)
				results[j.index] = res

				count := atomic.AddInt32(&completedCount, 1)
				mu.Lock()
				if res.Status == "ok" {
					fmt.Fprintf(os.Stderr, "[%d/%d] %s (%.1fs audio, %.0fms infer)\n",
						count, len(inputs), res.Input, res.Stats.AudioDurationSec, res.Stats.InferenceMs)
				} else {
					fmt.Fprintf(os.Stderr, "[%d/%d] %s FAILED: %s\n",
						count, len(inputs), res.Input, res.Error)
				}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	manifest := buildBatchManifest(inputs, results)

	if transcribeOutput != "" {
		if err := writeBatchOutput(transcribeOutput, manifest, jsonOutput()); err != nil {
			return fmt.Errorf("writing --output file: %w", err)
		}
	}

	if jsonOutput() {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(manifest); err != nil {
			return err
		}
	} else {
		printBatchTextSummary(manifest, modelLoadMs)
	}

	if manifest.Summary.FailedFiles > 0 {
		return fmt.Errorf("batch completed with %d failed file(s)", manifest.Summary.FailedFiles)
	}
	return nil
}

func processBatchItem(cmd *cobra.Command, tr *moonshine.Transcriber, input string) batchFileResult {
	res := batchFileResult{Input: input, Status: "ok"}
	localPath := input
	ctx := context.Background()

	var downloadMs float64
	if gcsfetch.IsGCSURI(input) {
		tmpDir, err := os.MkdirTemp("", "moonshine-gcs-*")
		if err != nil {
			res.Status = "failed"
			res.Error = err.Error()
			return res
		}
		defer os.RemoveAll(tmpDir)

		t0 := time.Now()
		var derr error
		localPath, derr = gcsfetch.Download(ctx, input, tmpDir)
		if derr != nil {
			res.Status = "failed"
			res.Error = derr.Error()
			return res
		}
		downloadMs = msSince(t0)
	}

	t0 := time.Now()
	samples, err := audio.LoadFile(localPath)
	if err != nil {
		res.Status = "failed"
		res.Error = err.Error()
		return res
	}
	decodeMs := msSince(t0)
	audioSec := float64(len(samples)) / float64(audio.TargetSampleRate)

	t0 = time.Now()
	transcript, err := tr.Transcribe(samples, audio.TargetSampleRate, 0)
	if err != nil {
		res.Status = "failed"
		res.Error = err.Error()
		return res
	}
	inferenceMs := msSince(t0)

	lines := transcript.Lines
	if !transcribeWithAudio {
		lines = make([]moonshine.Line, len(transcript.Lines))
		copy(lines, transcript.Lines)
		for i := range lines {
			lines[i].AudioData = nil
		}
	}

	rtf := 0.0
	if inferenceMs > 0 {
		rtf = audioSec / (inferenceMs / 1000.0)
	}

	var totalConf float64
	var lineCount int
	for _, l := range lines {
		if conf := l.MeanConfidence(); conf > 0 {
			totalConf += float64(conf)
			lineCount++
		}
	}
	meanConf := 0.0
	if lineCount > 0 {
		meanConf = totalConf / float64(lineCount)
	}

	res.Stats = batchFileStats{
		DownloadMs:       downloadMs,
		DecodeMs:         decodeMs,
		InferenceMs:      inferenceMs,
		AudioDurationSec: audioSec,
		RealTimeFactor:   rtf,
		MeanConfidence:   meanConf,
	}
	res.Lines = lines
	return res
}

func buildBatchManifest(inputs []string, results []batchFileResult) batchManifest {
	manifest := batchManifest{
		Version: "1.0",
		Results: results,
	}

	var totalAudioSec float64
	var totalInferMs float64
	var succCount, failCount int
	var confSum float64
	var confMin, confMax float64
	var confCount int

	for _, r := range results {
		if r.Status == "ok" {
			succCount++
			totalAudioSec += r.Stats.AudioDurationSec
			totalInferMs += r.Stats.InferenceMs
			if r.Stats.MeanConfidence > 0 {
				if confCount == 0 || r.Stats.MeanConfidence < confMin {
					confMin = r.Stats.MeanConfidence
				}
				if confCount == 0 || r.Stats.MeanConfidence > confMax {
					confMax = r.Stats.MeanConfidence
				}
				confSum += r.Stats.MeanConfidence
				confCount++
			}
		} else {
			failCount++
		}
	}

	rtf := 0.0
	if totalInferMs > 0 {
		rtf = totalAudioSec / (totalInferMs / 1000.0)
	}

	meanConf := 0.0
	if confCount > 0 {
		meanConf = confSum / float64(confCount)
	}

	manifest.Summary = batchSummary{
		TotalFiles:       len(inputs),
		SuccessfulFiles:  succCount,
		FailedFiles:      failCount,
		TotalAudioSec:    totalAudioSec,
		TotalInferenceMs: totalInferMs,
		AggregateRTF:     rtf,
		ConfidenceSummary: batchConfidenceSummary{
			Mean: meanConf,
			Min:  confMin,
			Max:  confMax,
		},
	}
	return manifest
}

func printBatchTextSummary(manifest batchManifest, modelLoadMs float64) {
	for _, r := range manifest.Results {
		fmt.Printf("\n=== %s ===\n", styleID.Render(r.Input))
		if r.Status != "ok" {
			fmt.Printf("ERROR: %s\n", styleFail.Render(r.Error))
			continue
		}
		for _, l := range r.Lines {
			prefix := fmt.Sprintf("[%6.2fs]", l.StartTime)
			if label := l.SpeakerLabel(); label != "" {
				prefix += " [" + label + "]"
			}
			fmt.Printf("%s %s\n", styleID.Render(prefix), l.Text)
			if transcribeWordTimestamps {
				if words := l.WordTimingsSummary(); words != "" {
					fmt.Printf("           %s\n", muted(words))
				}
			}
		}
	}
	fmt.Fprintln(os.Stderr, separator())
	fmt.Fprintf(os.Stderr, "%s batch=%d/%d files ok  audio=%.2fs infer=%.0fms aggregate_rtf=%.1fx  model_load=%.0fms\n",
		muted("stats:"), manifest.Summary.SuccessfulFiles, manifest.Summary.TotalFiles,
		manifest.Summary.TotalAudioSec, manifest.Summary.TotalInferenceMs, manifest.Summary.AggregateRTF, modelLoadMs)
	if transcribeOutput != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", muted("saved:"), transcribeOutput)
	}
}

func writeBatchOutput(path string, manifest batchManifest, asJSON bool) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if asJSON {
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		return enc.Encode(manifest)
	}

	for _, r := range manifest.Results {
		fmt.Fprintf(f, "=== %s ===\n", r.Input)
		if r.Status != "ok" {
			fmt.Fprintf(f, "ERROR: %s\n", r.Error)
			continue
		}
		for _, l := range r.Lines {
			fmt.Fprintf(f, "[%6.2fs] %s\n", l.StartTime, l.Text)
		}
	}
	return nil
}

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
