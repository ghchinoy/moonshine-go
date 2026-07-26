package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TranscribeInput defines arguments accepted by the 'transcribe' tool.
type TranscribeInput struct {
	Path             string `json:"path" jsonschema:"Path to local .wav audio file to transcribe in-process"`
	Language         string `json:"language,omitempty" jsonschema:"STT model language code (e.g. en, Spanish; default: en)"`
	Arch             string `json:"arch,omitempty" jsonschema:"STT model architecture (tiny, base; default: tiny)"`
	WordTimestamps   bool   `json:"word_timestamps,omitempty" jsonschema:"Include per-word timing details in line summaries"`
	IdentifySpeakers bool   `json:"identify_speakers,omitempty" jsonschema:"Enable speaker diarization"`
}

// TranscribeLine contains one transcribed sentence or utterance line.
type TranscribeLine struct {
	StartTime      float32 `json:"start_time"`
	Duration       float32 `json:"duration"`
	Speaker        string  `json:"speaker,omitempty"`
	Text           string  `json:"text"`
	MeanConfidence float64 `json:"mean_confidence"`
	WordSummary    string  `json:"word_summary,omitempty"`
}

// TranscribeStats holds performance timing metrics.
type TranscribeStats struct {
	AudioDurationSec float64 `json:"audio_duration_sec"`
	InferenceMs      float64 `json:"inference_ms"`
	RealTimeFactor   float64 `json:"real_time_factor"`
}

// TranscribeOutput is the structured JSON response returned by the tool.
type TranscribeOutput struct {
	Language string           `json:"language"`
	Arch     string           `json:"arch"`
	Lines    []TranscribeLine `json:"lines"`
	Stats    TranscribeStats  `json:"stats"`
	Note     string           `json:"note"`
}

// MCPServer manages the MCP server instance and in-process transcriber caching.
type MCPServer struct {
	mcpServer    *mcp.Server
	mu           sync.Mutex
	transcribers map[string]*moonshine.Transcriber
	defaultLang  string
	defaultArch  string
}

// NewMCPServer constructs an MCP server with the 'transcribe' tool bound.
func NewMCPServer(defaultLang, defaultArch string) *MCPServer {
	s := &MCPServer{
		transcribers: make(map[string]*moonshine.Transcriber),
		defaultLang:  defaultLang,
		defaultArch:  defaultArch,
	}

	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "moonshine-mcp-transcribe",
			Version: "v1.0.0",
		},
		nil,
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "transcribe",
			Description: "Transcribe local .wav audio files in-process using embedded Moonshine Speech-to-Text (purego C-API binding, zero cloud transmission).",
		},
		s.handleTranscribe,
	)

	s.mcpServer = server
	return s
}

// Close releases any cached transcriber resources.
func (s *MCPServer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tr := range s.transcribers {
		_ = tr.Close()
	}
	s.transcribers = make(map[string]*moonshine.Transcriber)
}

func normalizeSTTLanguage(lang string) string {
	if lang == "en_us" || lang == "en_gb" || lang == "en-US" || lang == "en-GB" {
		return "en"
	}
	if lang == "" {
		return "en"
	}
	return lang
}

func (s *MCPServer) getTranscriber(rawLang, arch string, identifySpeakers bool) (*moonshine.Transcriber, string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lang := normalizeSTTLanguage(rawLang)
	if arch == "" {
		arch = s.defaultArch
	}

	key := fmt.Sprintf("%s-%s-diar:%t", lang, arch, identifySpeakers)
	if tr, ok := s.transcribers[key]; ok {
		return tr, lang, arch, nil
	}

	libDir := os.Getenv("MOONSHINE_LIB_DIR")
	if err := moonshine.Load(libDir); err != nil {
		return nil, lang, arch, fmt.Errorf("loading libmoonshine: %w (ensure MOONSHINE_LIB_DIR is set)", err)
	}

	modelDir := resolveModelDir(lang, arch)
	if modelDir == "" {
		hint := ""
		if rawLang == "en_us" || rawLang == "en_gb" {
			hint = fmt.Sprintf(" (Note: STT uses short language codes like 'en', whereas TTS uses tags like 'en_us').")
		}
		return nil, lang, arch, fmt.Errorf("STT model for %s/%s not found%s (run 'moonshine setup --language %s --arch %s' first)", lang, arch, hint, lang, arch)
	}

	archID := moonshine.ModelArchTiny
	if arch == "tiny-streaming" {
		archID = moonshine.ModelArchTinyStreaming
	} else if arch == "base" {
		archID = moonshine.ModelArchBase
	} else if arch == "base-streaming" {
		archID = moonshine.ModelArchBaseStreaming
	}

	var opts []moonshine.Option
	if identifySpeakers {
		opts = append(opts, moonshine.Option{Name: "identify_speakers", Value: "1"})
	}

	tr, err := moonshine.LoadTranscriber(modelDir, archID, opts...)
	if err != nil {
		return nil, lang, arch, fmt.Errorf("loading transcriber from %s: %w", modelDir, err)
	}

	s.transcribers[key] = tr
	return tr, lang, arch, nil
}

func (s *MCPServer) handleTranscribe(ctx context.Context, req *mcp.CallToolRequest, input TranscribeInput) (*mcp.CallToolResult, TranscribeOutput, error) {
	if input.Path == "" {
		return nil, TranscribeOutput{}, fmt.Errorf("path is required")
	}

	tr, lang, arch, err := s.getTranscriber(input.Language, input.Arch, input.IdentifySpeakers)
	if err != nil {
		return nil, TranscribeOutput{}, err
	}

	samples, sampleRate, err := loadWAVSamples(input.Path)
	if err != nil {
		return nil, TranscribeOutput{}, fmt.Errorf("reading WAV audio from %s: %w", input.Path, err)
	}

	audioSec := float64(len(samples)) / float64(sampleRate)

	t0 := time.Now()
	transcript, err := tr.Transcribe(samples, int32(sampleRate), 0)
	inferenceMs := float64(time.Since(t0).Milliseconds())
	if err != nil {
		return nil, TranscribeOutput{}, fmt.Errorf("transcribing audio: %w", err)
	}

	rtf := 0.0
	if inferenceMs > 0 {
		rtf = audioSec / (inferenceMs / 1000.0)
	}

	outLines := make([]TranscribeLine, 0, len(transcript.Lines))
	for _, l := range transcript.Lines {
		summary := ""
		if input.WordTimestamps && len(l.Words) > 0 {
			summary = l.WordTimingsSummary()
		}

		outLines = append(outLines, TranscribeLine{
			StartTime:      l.StartTime,
			Duration:       l.Duration,
			Speaker:        l.SpeakerLabel(),
			Text:           l.Text,
			MeanConfidence: float64(l.MeanConfidence()),
			WordSummary:    summary,
		})
	}

	out := TranscribeOutput{
		Language: lang,
		Arch:     arch,
		Lines:    outLines,
		Stats: TranscribeStats{
			AudioDurationSec: audioSec,
			InferenceMs:      inferenceMs,
			RealTimeFactor:   rtf,
		},
		Note: "Audio is transcribed 100% locally in-process via pkg/moonshine. No raw audio data is uploaded or transmitted to external servers.",
	}

	return nil, out, nil
}
