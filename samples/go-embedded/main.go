package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
)

func main() {
	var (
		audioFlag            string
		streamFlag           bool
		langFlag             string
		archFlag             string
		wordTimestampsFlag   bool
		identifySpeakersFlag bool
	)

	flag.StringVar(&audioFlag, "audio", "", "Path to .wav audio file to transcribe in-process")
	flag.BoolVar(&streamFlag, "stream", false, "Demonstrate streaming API (NewStream) in-process with chunked audio ingestion")
	flag.StringVar(&langFlag, "language", "en", "STT model language")
	flag.StringVar(&archFlag, "arch", "tiny", "STT model architecture (e.g. tiny, tiny-streaming)")
	flag.BoolVar(&wordTimestampsFlag, "word-timestamps", false, "Enable per-word timestamps")
	flag.BoolVar(&identifySpeakersFlag, "identify-speakers", false, "Enable speaker diarization")
	flag.Parse()

	if audioFlag == "" {
		fmt.Fprintf(os.Stderr, "Usage of go-embedded (in-process STT via pkg/moonshine):\n")
		fmt.Fprintf(os.Stderr, "  go run . -audio <path_to_wav> [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// 1. Load native libmoonshine shared library via purego (no cgo)
	fmt.Fprintf(os.Stderr, "[go-embedded] Loading libmoonshine via purego...\n")
	libPath := os.Getenv("MOONSHINE_LIB_DIR")
	if err := moonshine.Load(libPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading libmoonshine: %v\n", err)
		fmt.Fprintf(os.Stderr, "Ensure MOONSHINE_LIB_DIR points to the directory containing libmoonshine.{dylib,so}.\n")
		os.Exit(1)
	}

	// 2. Load STT Transcriber model
	modelDir := resolveModelDir(langFlag, archFlag)
	if modelDir == "" {
		fmt.Fprintf(os.Stderr, "Error: STT model for %s/%s not found.\n", langFlag, archFlag)
		fmt.Fprintf(os.Stderr, "Run 'moonshine setup --language %s --arch %s' first.\n", langFlag, archFlag)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "[go-embedded] Loading model from %s...\n", modelDir)
	var opts []moonshine.Option
	if identifySpeakersFlag {
		opts = append(opts, moonshine.Option{Name: "identify_speakers", Value: "1"})
	}

	// ModelArchTiny = 0, ModelArchBase = 1, ModelArchTinyStreaming = 2
	archID := moonshine.ModelArchTiny
	if archFlag == "tiny-streaming" {
		archID = moonshine.ModelArchTinyStreaming
	} else if archFlag == "base" {
		archID = moonshine.ModelArchBase
	}

	tr, err := moonshine.LoadTranscriber(modelDir, archID, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading transcriber: %v\n", err)
		os.Exit(1)
	}
	defer tr.Close()

	// 3. Load WAV audio samples (mono float32, 16kHz)
	samples, sampleRate, err := loadWAVSamples(audioFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading WAV file %s: %v\n", audioFlag, err)
		os.Exit(1)
	}

	audioDurationSec := float64(len(samples)) / float64(sampleRate)
	fmt.Fprintf(os.Stderr, "[go-embedded] Loaded %.2fs audio (sample rate %dHz)\n", audioDurationSec, sampleRate)

	if streamFlag {
		runStreamingInProcess(tr, samples, int32(sampleRate), audioDurationSec)
	} else {
		runBatchInProcess(tr, samples, int32(sampleRate), audioDurationSec)
	}
}

func runBatchInProcess(tr *moonshine.Transcriber, samples []float32, sampleRate int32, audioDurationSec float64) {
	fmt.Fprintf(os.Stderr, "[go-embedded] Transcribing in batch mode...\n\n")

	t0 := time.Now()
	transcript, err := tr.Transcribe(samples, sampleRate, 0)
	inferenceMs := float64(time.Since(t0).Milliseconds())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Transcription error: %v\n", err)
		os.Exit(1)
	}

	rtf := 0.0
	if inferenceMs > 0 {
		rtf = audioDurationSec / (inferenceMs / 1000.0)
	}

	fmt.Println("=== In-Process Transcript ===")
	for _, line := range transcript.Lines {
		prefix := fmt.Sprintf("[%02d:%02d]", int(line.StartTime)/60, int(line.StartTime)%60)
		if label := line.SpeakerLabel(); label != "" {
			prefix += " [" + label + "]"
		}
		fmt.Printf("%s %s (conf: %.0f%%)\n", prefix, line.Text, line.MeanConfidence()*100)
		if len(line.Words) > 0 {
			if summary := line.WordTimingsSummary(); summary != "" {
				fmt.Printf("        %s\n", summary)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\n[stats] audio=%.2fs infer=%.0fms rtf=%.1fx\n", audioDurationSec, inferenceMs, rtf)
}

func runStreamingInProcess(tr *moonshine.Transcriber, samples []float32, sampleRate int32, audioDurationSec float64) {
	fmt.Fprintf(os.Stderr, "[go-embedded] Demonstrating in-process streaming API (NewStream)...\n\n")

	stream, err := tr.NewStream(0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating stream: %v\n", err)
		os.Exit(1)
	}
	defer stream.Close()

	if err := stream.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting stream: %v\n", err)
		os.Exit(1)
	}
	defer stream.Stop()

	chunkSize := int(sampleRate) / 10 // 100ms PCM chunks
	t0 := time.Now()

	for i := 0; i < len(samples); i += chunkSize {
		end := i + chunkSize
		if end > len(samples) {
			end = len(samples)
		}

		if err := stream.AddAudio(samples[i:end], sampleRate); err != nil {
			fmt.Fprintf(os.Stderr, "Error adding audio to stream: %v\n", err)
			break
		}

		transcript, err := stream.Transcribe(0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error transcribing stream: %v\n", err)
			break
		}

		if len(transcript.Lines) > 0 {
			latest := transcript.Lines[len(transcript.Lines)-1]
			status := "[interim]"
			if latest.IsComplete {
				status = "[FINAL]  "
			}
			fmt.Printf("%s [%02d:%02d] %s\n", status, int(latest.StartTime)/60, int(latest.StartTime)%60, latest.Text)
		}
	}

	inferenceMs := float64(time.Since(t0).Milliseconds())
	fmt.Fprintf(os.Stderr, "\n[stats] streaming complete in %.0fms\n", inferenceMs)
}

func resolveModelDir(lang, arch string) string {
	cacheRoot := os.Getenv("MOONSHINE_VOICE_CACHE")
	if cacheRoot == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cacheRoot = filepath.Join(home, "Library", "Caches", "moonshine_voice")
			if _, err := os.Stat(cacheRoot); os.IsNotExist(err) {
				cacheRoot = filepath.Join(home, ".cache", "moonshine_voice")
			}
		}
	}

	modelName := fmt.Sprintf("%s-%s", arch, lang)
	candidates := []string{
		filepath.Join(cacheRoot, "download.moonshine.ai", "model", modelName, "quantized", modelName),
		filepath.Join(cacheRoot, "download.moonshine.ai", "model", modelName, "quantized"),
		filepath.Join(cacheRoot, "download.moonshine.ai", "model", modelName),
		filepath.Join(cacheRoot, modelName),
	}

	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return ""
}

// Minimal 16kHz WAV decoder to keep go-embedded zero-dependency beyond stdlib + pkg/moonshine
func loadWAVSamples(path string) ([]float32, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("invalid WAV file header")
	}

	numChannels := int(data[22]) | (int(data[23]) << 8)
	sampleRate := int(data[24]) | (int(data[25]) << 8) | (int(data[26]) << 16) | (int(data[27]) << 24)
	bitsPerSample := int(data[34]) | (int(data[35]) << 8)

	dataChunkOffset := 36
	for dataChunkOffset+8 < len(data) {
		if string(data[dataChunkOffset:dataChunkOffset+4]) == "data" {
			break
		}
		chunkSize := int(data[dataChunkOffset+4]) | (int(data[dataChunkOffset+5]) << 8) | (int(data[dataChunkOffset+6]) << 16) | (int(data[dataChunkOffset+7]) << 24)
		dataChunkOffset += 8 + chunkSize
	}

	if dataChunkOffset+8 >= len(data) {
		return nil, 0, fmt.Errorf("data chunk not found in WAV")
	}

	dataSize := int(data[dataChunkOffset+4]) | (int(data[dataChunkOffset+5]) << 8) | (int(data[dataChunkOffset+6]) << 16) | (int(data[dataChunkOffset+7]) << 24)
	pcmData := data[dataChunkOffset+8:]
	if len(pcmData) > dataSize {
		pcmData = pcmData[:dataSize]
	}

	bytesPerSample := bitsPerSample / 8
	if bytesPerSample == 0 {
		return nil, 0, fmt.Errorf("unsupported bitsPerSample %d", bitsPerSample)
	}

	totalSamples := len(pcmData) / (bytesPerSample * numChannels)
	samples := make([]float32, totalSamples)

	for i := 0; i < totalSamples; i++ {
		offset := i * bytesPerSample * numChannels
		var val float32
		if bitsPerSample == 16 {
			raw := int16(int(pcmData[offset]) | (int(pcmData[offset+1]) << 8))
			val = float32(raw) / 32768.0
		} else if bitsPerSample == 32 {
			raw := int32(int(pcmData[offset]) | (int(pcmData[offset+1]) << 8) | (int(pcmData[offset+2]) << 16) | (int(pcmData[offset+3]) << 24))
			val = float32(raw) / 2147483648.0
		}
		samples[i] = val
	}

	return samples, sampleRate, nil
}
