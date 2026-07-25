package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// TranscribeStats holds timing and throughput metrics from moonshine transcribe --json.
type TranscribeStats struct {
	ModelLoadMs      float64 `json:"model_load_ms"`
	DownloadMs       float64 `json:"download_ms,omitempty"`
	DecodeMs         float64 `json:"decode_ms"`
	InferenceMs      float64 `json:"inference_ms"`
	AudioDurationSec float64 `json:"audio_duration_sec"`
	RealTimeFactor   float64 `json:"real_time_factor"`
}

// FileResult holds the transcription and stats for a single audio file.
type FileResult struct {
	FilePath       string          `json:"file_path"`
	FileName       string          `json:"file_name"`
	Lines          []serveapi.Line `json:"lines"`
	Stats          TranscribeStats `json:"stats"`
	MeanConfidence float32         `json:"mean_confidence"`
	Error          string          `json:"error,omitempty"`
}

// AggregateStats holds summary metrics across the entire corpus.
type AggregateStats struct {
	TotalFiles           int            `json:"total_files"`
	SuccessfulFiles      int            `json:"successful_files"`
	FailedFiles          int            `json:"failed_files"`
	TotalAudioSec        float64        `json:"total_audio_sec"`
	TotalInferenceMs     float64        `json:"total_inference_ms"`
	TotalWallMs          float64        `json:"total_wall_ms"`
	AggregateRTF         float64        `json:"aggregate_rtf"`
	MeanConfidence       float32        `json:"mean_confidence"`
	ConfidenceHistogram  map[string]int `json:"confidence_histogram"`
}

// CorpusManifest is the agent-friendly JSON manifest structure.
type CorpusManifest struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Stats       AggregateStats `json:"aggregate_stats"`
	Files       []FileResult   `json:"files"`
	Analysis    *AnalysisReport `json:"analysis,omitempty"`
}

// AnalysisReport holds the LLM-generated synthesis.
type AnalysisReport struct {
	ModelUsed       string   `json:"model_used"`
	Themes          []string `json:"themes,omitempty"`
	CrossSummary    string   `json:"cross_summary"`
	MarkdownContent string   `json:"markdown_content"`
}

func main() {
	var (
		dirFlag              string
		binaryFlag           string
		concurrencyFlag      int
		outMDFlag            string
		outJSONFlag          string
		noLLMFlag            bool
		geminiKeyFlag        string
		geminiModelFlag      string
		wordTimestampsFlag   bool
		identifySpeakersFlag bool
		langFlag             string
		archFlag             string
	)

	flag.StringVar(&dirFlag, "dir", "", "Directory containing .wav audio files to transcribe in bulk")
	flag.StringVar(&binaryFlag, "binary", "", "Path to moonshine CLI binary (default: searches ./bin/moonshine or PATH)")
	flag.IntVar(&concurrencyFlag, "concurrency", 4, "Maximum parallel transcription workers")
	flag.StringVar(&outMDFlag, "out-md", "corpus_report.md", "Output path for the Markdown analysis report")
	flag.StringVar(&outJSONFlag, "out-json", "corpus_manifest.json", "Output path for the agent-friendly JSON manifest")
	flag.BoolVar(&noLLMFlag, "no-llm", false, "Skip the Gemini LLM analysis pass (transcribe and compute stats only)")
	flag.StringVar(&geminiKeyFlag, "gemini-key", "", "Gemini API key (defaults to GEMINI_API_KEY or GOOGLE_API_KEY env var)")
	flag.StringVar(&geminiModelFlag, "gemini-model", "gemini-2.5-flash", "Gemini model to use for analysis")
	flag.BoolVar(&wordTimestampsFlag, "word-timestamps", true, "Include word-level timestamps in transcription output")
	flag.BoolVar(&identifySpeakersFlag, "identify-speakers", false, "Enable speaker diarization in transcription output")
	flag.StringVar(&langFlag, "language", "en", "STT model language")
	flag.StringVar(&archFlag, "arch", "tiny", "STT model architecture (e.g. tiny, base)")
	flag.Parse()

	if dirFlag == "" {
		fmt.Fprintf(os.Stderr, "Usage of go-bulk-analysis:\n")
		fmt.Fprintf(os.Stderr, "  go run . -dir <audio_directory> [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	binaryPath := resolveBinary(binaryFlag)
	if binaryPath == "" {
		fmt.Fprintf(os.Stderr, "Error: moonshine binary not found. Build it with 'make build' at repo root or pass -binary.\n")
		os.Exit(1)
	}

	files, err := findWAVFiles(dirFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding WAV files in %s: %v\n", dirFlag, err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "No .wav files found in %s\n", dirFlag)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "[bulk-analysis] Found %d .wav files in %s\n", len(files), dirFlag)
	fmt.Fprintf(os.Stderr, "[bulk-analysis] Transcribing with concurrency=%d using binary %s...\n", concurrencyFlag, binaryPath)

	tStart := time.Now()
	results := transcribeCorpus(files, binaryPath, concurrencyFlag, langFlag, archFlag, wordTimestampsFlag, identifySpeakersFlag)
	wallDuration := time.Since(tStart)

	manifest := buildCorpusManifest(results, wallDuration)

	fmt.Fprintf(os.Stderr, "\n[bulk-analysis] Transcription complete:\n")
	fmt.Fprintf(os.Stderr, "  Total Audio:  %.2fs (%.1f min)\n", manifest.Stats.TotalAudioSec, manifest.Stats.TotalAudioSec/60.0)
	fmt.Fprintf(os.Stderr, "  Wall Clock:   %.2fs\n", wallDuration.Seconds())
	fmt.Fprintf(os.Stderr, "  Overall RTF:  %.1fx real-time\n", manifest.Stats.AggregateRTF)
	fmt.Fprintf(os.Stderr, "  Mean Conf:    %.2f%%\n", manifest.Stats.MeanConfidence*100)
	fmt.Fprintf(os.Stderr, "  Confidence Hist: >=90%%: %d, 80-89%%: %d, <80%%: %d\n",
		manifest.Stats.ConfidenceHistogram[">=90%"],
		manifest.Stats.ConfidenceHistogram["80-89%"],
		manifest.Stats.ConfidenceHistogram["<80%"])

	apiKey := resolveGeminiKey(geminiKeyFlag)
	if noLLMFlag {
		fmt.Fprintf(os.Stderr, "[bulk-analysis] Skipping LLM pass (-no-llm set)\n")
	} else if apiKey == "" {
		fmt.Fprintf(os.Stderr, "[bulk-analysis] Skipping LLM pass (no GEMINI_API_KEY or GOOGLE_API_KEY found)\n")
	} else {
		fmt.Fprintf(os.Stderr, "\n[bulk-analysis] Running Gemini analysis pass (%s)...\n", geminiModelFlag)
		analysis, err := runGeminiAnalysis(context.Background(), apiKey, geminiModelFlag, manifest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Gemini analysis failed: %v\n", err)
		} else {
			manifest.Analysis = analysis
			fmt.Fprintf(os.Stderr, "[bulk-analysis] Gemini analysis completed successfully.\n")
		}
	}

	if err := writeJSONManifest(outJSONFlag, manifest); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON manifest: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[bulk-analysis] Saved JSON manifest: %s\n", outJSONFlag)
	}

	if err := writeMarkdownReport(outMDFlag, manifest); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing Markdown report: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[bulk-analysis] Saved Markdown report: %s\n", outMDFlag)
	}
}

func resolveBinary(custom string) string {
	if custom != "" {
		if _, err := os.Stat(custom); err == nil {
			return custom
		}
	}
	candidates := []string{
		"../../bin/moonshine",
		"./bin/moonshine",
		"moonshine",
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func findWAVFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".wav") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func transcribeCorpus(files []string, binary string, concurrency int, lang, arch string, wordTs, speakers bool) []FileResult {
	results := make([]FileResult, len(files))
	jobs := make(chan int, len(files))
	for i := range files {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	workers := concurrency
	if workers > len(files) {
		workers = len(files)
	}

	var progressMu sync.Mutex
	completed := 0

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				fpath := files[idx]
				res := transcribeOneFile(binary, fpath, lang, arch, wordTs, speakers)
				results[idx] = res

				progressMu.Lock()
				completed++
				status := fmt.Sprintf("[%d/%d] %s", completed, len(files), filepath.Base(fpath))
				if res.Error != "" {
					fmt.Fprintf(os.Stderr, "%s FAILED: %s\n", status, res.Error)
				} else {
					fmt.Fprintf(os.Stderr, "%s -> %.2fs audio transcribed in %.0fms (%.1fx RTF, conf %.0f%%)\n",
						status, res.Stats.AudioDurationSec, res.Stats.InferenceMs, res.Stats.RealTimeFactor, res.MeanConfidence*100)
				}
				progressMu.Unlock()
			}
		}()
	}

	wg.Wait()
	return results
}

type cliOutput struct {
	Lines []serveapi.Line `json:"lines"`
	Stats TranscribeStats `json:"stats"`
}

func transcribeOneFile(binary, fpath, lang, arch string, wordTs, speakers bool) FileResult {
	res := FileResult{
		FilePath: fpath,
		FileName: filepath.Base(fpath),
	}

	args := []string{"transcribe", fpath, "--json", "--language", lang, "--arch", arch}
	if wordTs {
		args = append(args, "--word-timestamps")
	}
	if speakers {
		args = append(args, "--identify-speakers")
	}

	cmd := exec.Command(binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		res.Error = fmt.Sprintf("exec error: %v (stderr: %s)", err, strings.TrimSpace(stderr.String()))
		return res
	}

	var parsed cliOutput
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		res.Error = fmt.Sprintf("json parse error: %v", err)
		return res
	}

	res.Lines = parsed.Lines
	res.Stats = parsed.Stats

	var confSum float32
	var wordCount int
	for _, line := range res.Lines {
		if len(line.Words) > 0 {
			for _, w := range line.Words {
				confSum += w.Confidence
				wordCount++
			}
		} else if line.Confidence > 0 {
			confSum += line.Confidence
			wordCount++
		}
	}
	if wordCount > 0 {
		res.MeanConfidence = confSum / float32(wordCount)
	}

	return res
}

func buildCorpusManifest(results []FileResult, wallDuration time.Duration) CorpusManifest {
	var (
		totalAudio float64
		totalInfer float64
		confSum    float32
		confCount  int
		succCount  int
		failCount  int
		hist       = map[string]int{">=90%": 0, "80-89%": 0, "<80%": 0}
	)

	for _, r := range results {
		if r.Error != "" {
			failCount++
			continue
		}
		succCount++
		totalAudio += r.Stats.AudioDurationSec
		totalInfer += r.Stats.InferenceMs

		if r.MeanConfidence > 0 {
			confSum += r.MeanConfidence
			confCount++
			if r.MeanConfidence >= 0.90 {
				hist[">=90%"]++
			} else if r.MeanConfidence >= 0.80 {
				hist["80-89%"]++
			} else {
				hist["<80%"]++
			}
		}
	}

	var meanConf float32
	if confCount > 0 {
		meanConf = confSum / float32(confCount)
	}

	wallMs := float64(wallDuration.Milliseconds())
	var aggRTF float64
	if wallMs > 0 {
		aggRTF = totalAudio / (wallMs / 1000.0)
	}

	return CorpusManifest{
		GeneratedAt: time.Now().UTC(),
		Stats: AggregateStats{
			TotalFiles:          len(results),
			SuccessfulFiles:     succCount,
			FailedFiles:         failCount,
			TotalAudioSec:       totalAudio,
			TotalInferenceMs:    totalInfer,
			TotalWallMs:         wallMs,
			AggregateRTF:        aggRTF,
			MeanConfidence:      meanConf,
			ConfidenceHistogram: hist,
		},
		Files: results,
	}
}

func resolveGeminiKey(flagKey string) string {
	if flagKey != "" {
		return flagKey
	}
	if k := os.Getenv("GEMINI_API_KEY"); k != "" {
		return k
	}
	return os.Getenv("GOOGLE_API_KEY")
}

func runGeminiAnalysis(ctx context.Context, apiKey, model string, manifest CorpusManifest) (*AnalysisReport, error) {
	var corpusBuilder strings.Builder
	corpusBuilder.WriteString("The following is a transcript corpus from multiple audio recordings:\n\n")

	for _, f := range manifest.Files {
		if f.Error != "" {
			continue
		}
		corpusBuilder.WriteString(fmt.Sprintf("=== RECORDING: %s (Duration: %.1fs, Conf: %.0f%%) ===\n",
			f.FileName, f.Stats.AudioDurationSec, f.MeanConfidence*100))
		for _, l := range f.Lines {
			speakerPrefix := ""
			if label := l.SpeakerLabel(); label != "" {
				speakerPrefix = "[" + label + "] "
			}
			timeStr := fmt.Sprintf("[%02d:%02d]", int(l.StartTime)/60, int(l.StartTime)%60)
			corpusBuilder.WriteString(fmt.Sprintf("%s %s%s (conf: %.0f%%)\n", timeStr, speakerPrefix, l.Text, l.Confidence*100))
		}
		corpusBuilder.WriteString("\n")
	}

	prompt := fmt.Sprintf(`You are an expert qualitative research assistant analyzing an audio transcript corpus.

%s

Please produce a structured analysis report with the following sections in Markdown:

1. **Executive Synthesis**: A concise 2-3 paragraph cross-recording synthesis of the core topics, insights, and findings across all recordings.
2. **Key Themes**: 3-5 distinct thematic clusters with bullet points explaining each.
3. **Notable Quotes with Citations**: 4-8 verbatim or near-verbatim quotes from the recordings that highlight key insights. For EACH quote, include a precise citation formatted as:
   > "exact quote text"
   *— Source: [filename @ MM:SS (confidence: X%%)]*
4. **Per-Recording Summaries**: A short 2-sentence summary for each individual recording in the corpus.

Format your output as clean, publishable Markdown.`, corpusBuilder.String())

	reqBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": prompt},
				},
			},
		},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gemini API error (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty candidates returned from Gemini API")
	}

	mdText := geminiResp.Candidates[0].Content.Parts[0].Text

	return &AnalysisReport{
		ModelUsed:       model,
		MarkdownContent: mdText,
	}, nil
}

func writeJSONManifest(path string, manifest CorpusManifest) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(manifest)
}

func writeMarkdownReport(path string, manifest CorpusManifest) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var buf bytes.Buffer
	buf.WriteString("# Bulk Audio Analysis Report\n\n")
	buf.WriteString(fmt.Sprintf("*Generated at: %s*\n\n", manifest.GeneratedAt.Format(time.RFC1123)))

	buf.WriteString("## Corpus Throughput & Performance\n\n")
	buf.WriteString("| Metric | Value |\n")
	buf.WriteString("|---|---|\n")
	buf.WriteString(fmt.Sprintf("| **Total Recordings** | %d files (%d successful, %d failed) |\n", manifest.Stats.TotalFiles, manifest.Stats.SuccessfulFiles, manifest.Stats.FailedFiles))
	buf.WriteString(fmt.Sprintf("| **Total Audio Duration** | %.2f seconds (%.1f minutes) |\n", manifest.Stats.TotalAudioSec, manifest.Stats.TotalAudioSec/60.0))
	buf.WriteString(fmt.Sprintf("| **Wall-Clock Time** | %.2f seconds |\n", manifest.Stats.TotalWallMs/1000.0))
	buf.WriteString(fmt.Sprintf("| **Aggregate Speed (RTF)** | **%.1fx real-time** |\n", manifest.Stats.AggregateRTF))
	buf.WriteString(fmt.Sprintf("| **Mean STT Confidence** | **%.1f%%** |\n", manifest.Stats.MeanConfidence*100))
	buf.WriteString(fmt.Sprintf("| **Confidence Distribution** | >=90%%: %d, 80-89%%: %d, <80%%: %d |\n\n",
		manifest.Stats.ConfidenceHistogram[">=90%"],
		manifest.Stats.ConfidenceHistogram["80-89%"],
		manifest.Stats.ConfidenceHistogram["<80%"]))

	if manifest.Analysis != nil && manifest.Analysis.MarkdownContent != "" {
		buf.WriteString("---\n\n")
		buf.WriteString(manifest.Analysis.MarkdownContent)
		buf.WriteString("\n")
	} else {
		buf.WriteString("---\n\n")
		buf.WriteString("## Transcribed Corpus Summary\n\n")
		for _, file := range manifest.Files {
			if file.Error != "" {
				buf.WriteString(fmt.Sprintf("### ❌ %s\n*Error: %s*\n\n", file.FileName, file.Error))
				continue
			}
			buf.WriteString(fmt.Sprintf("### 📁 %s\n", file.FileName))
			buf.WriteString(fmt.Sprintf("*Duration: %.1fs | Inference: %.0fms (%.1fx RTF) | Mean Confidence: %.0f%%*\n\n",
				file.Stats.AudioDurationSec, file.Stats.InferenceMs, file.Stats.RealTimeFactor, file.MeanConfidence*100))
			for _, line := range file.Lines {
				prefix := fmt.Sprintf("[%02d:%02d]", int(line.StartTime)/60, int(line.StartTime)%60)
				if label := line.SpeakerLabel(); label != "" {
					prefix += " [" + label + "]"
				}
				buf.WriteString(fmt.Sprintf("- `%s` %s *(conf: %.0f%%)*\n", prefix, line.Text, line.Confidence*100))
			}
			buf.WriteString("\n")
		}
	}

	_, err = f.Write(buf.Bytes())
	return err
}
