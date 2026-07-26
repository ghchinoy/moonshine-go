package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
)

// LineOutput represents a line of transcription returned to the frontend.
type LineOutput struct {
	ID             uint64  `json:"id"`
	StartTime      float32 `json:"start_time"`
	Duration       float32 `json:"duration"`
	Text           string  `json:"text"`
	IsComplete     bool    `json:"is_complete"`
	MeanConfidence float64 `json:"mean_confidence"`
}

// BatchOutput represents the result of transcribing a WAV file.
type BatchOutput struct {
	Lines            []LineOutput `json:"lines"`
	AudioDurationSec float64      `json:"audio_duration_sec"`
	InferenceMs      float64      `json:"inference_ms"`
	RealTimeFactor   float64      `json:"real_time_factor"`
}

// App struct manages Wails lifecycle and in-process Moonshine STT engine.
type App struct {
	ctx          context.Context
	mu           sync.Mutex
	transcribers map[string]*moonshine.Transcriber
	activeStream *moonshine.Stream
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{
		transcribers: make(map[string]*moonshine.Transcriber),
	}
}

// startup is called when the app starts. Loads libmoonshine dynamically.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	libDir := os.Getenv("MOONSHINE_LIB_DIR")
	if err := moonshine.Load(libDir); err != nil {
		fmt.Fprintf(os.Stderr, "[desktop-app] Warning loading libmoonshine at startup: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[desktop-app] Successfully loaded libmoonshine via purego.\n")
	}
}

func (a *App) getTranscriber(lang, arch string) (*moonshine.Transcriber, error) {
	if lang == "" {
		lang = "en_us"
	}
	if arch == "" {
		arch = "tiny"
	}

	key := fmt.Sprintf("%s-%s", lang, arch)
	if tr, ok := a.transcribers[key]; ok {
		return tr, nil
	}

	libDir := os.Getenv("MOONSHINE_LIB_DIR")
	if err := moonshine.Load(libDir); err != nil {
		return nil, fmt.Errorf("loading libmoonshine: %w (set MOONSHINE_LIB_DIR)", err)
	}

	modelDir := resolveModelDir(lang, arch)
	if modelDir == "" {
		return nil, fmt.Errorf("model for %s/%s not found (run 'moonshine setup --language %s --arch %s')", lang, arch, lang, arch)
	}

	archID := moonshine.ModelArchTiny
	if arch == "tiny-streaming" {
		archID = moonshine.ModelArchTinyStreaming
	} else if arch == "base" {
		archID = moonshine.ModelArchBase
	} else if arch == "base-streaming" {
		archID = moonshine.ModelArchBaseStreaming
	}

	tr, err := moonshine.LoadTranscriber(modelDir, archID)
	if err != nil {
		return nil, fmt.Errorf("loading transcriber from %s: %w", modelDir, err)
	}

	a.transcribers[key] = tr
	return tr, nil
}

// StartStream initializes an in-process streaming transcription session.
func (a *App) StartStream(language, arch string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.activeStream != nil {
		_ = a.activeStream.Stop()
		_ = a.activeStream.Close()
		a.activeStream = nil
	}

	if arch == "" {
		arch = "tiny-streaming"
	}

	tr, err := a.getTranscriber(language, arch)
	if err != nil {
		return err
	}

	st, err := tr.NewStream(0)
	if err != nil {
		return fmt.Errorf("creating stream: %w", err)
	}

	if err := st.Start(); err != nil {
		_ = st.Close()
		return fmt.Errorf("starting stream: %w", err)
	}

	a.activeStream = st
	return nil
}

// PushPCMChunk appends a float32 PCM chunk (16kHz mono) to the active stream
// and returns updated lines.
func (a *App) PushPCMChunk(samples []float32) ([]LineOutput, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.activeStream == nil {
		return nil, fmt.Errorf("no active streaming session")
	}

	if len(samples) > 0 {
		if err := a.activeStream.AddAudio(samples, 16000); err != nil {
			return nil, fmt.Errorf("adding audio chunk: %w", err)
		}
	}

	tr, err := a.activeStream.Transcribe(0)
	if err != nil {
		return nil, fmt.Errorf("transcribing stream: %w", err)
	}

	out := make([]LineOutput, 0, len(tr.Lines))
	for _, l := range tr.Lines {
		out = append(out, LineOutput{
			ID:             l.ID,
			StartTime:      l.StartTime,
			Duration:       l.Duration,
			Text:           l.Text,
			IsComplete:     l.IsComplete,
			MeanConfidence: float64(l.MeanConfidence()),
		})
	}

	return out, nil
}

// StopStream stops and closes the active streaming session.
func (a *App) StopStream() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.activeStream != nil {
		_ = a.activeStream.Stop()
		_ = a.activeStream.Close()
		a.activeStream = nil
	}
	return nil
}

// TranscribeFile transcribes a local WAV file in-process in batch mode.
func (a *App) TranscribeFile(filePath, language, arch string) (BatchOutput, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if filePath == "" {
		return BatchOutput{}, fmt.Errorf("filePath is required")
	}

	tr, err := a.getTranscriber(language, arch)
	if err != nil {
		return BatchOutput{}, err
	}

	samples, sampleRate, err := loadWAVSamples(filePath)
	if err != nil {
		return BatchOutput{}, fmt.Errorf("reading WAV audio %s: %w", filePath, err)
	}

	audioSec := float64(len(samples)) / float64(sampleRate)

	t0 := time.Now()
	transcript, err := tr.Transcribe(samples, int32(sampleRate), 0)
	inferenceMs := float64(time.Since(t0).Milliseconds())
	if err != nil {
		return BatchOutput{}, fmt.Errorf("transcribing audio: %w", err)
	}

	rtf := 0.0
	if inferenceMs > 0 {
		rtf = audioSec / (inferenceMs / 1000.0)
	}

	outLines := make([]LineOutput, 0, len(transcript.Lines))
	for _, l := range transcript.Lines {
		outLines = append(outLines, LineOutput{
			ID:             l.ID,
			StartTime:      l.StartTime,
			Duration:       l.Duration,
			Text:           l.Text,
			IsComplete:     l.IsComplete,
			MeanConfidence: float64(l.MeanConfidence()),
		})
	}

	return BatchOutput{
		Lines:            outLines,
		AudioDurationSec: audioSec,
		InferenceMs:      inferenceMs,
		RealTimeFactor:   rtf,
	}, nil
}
