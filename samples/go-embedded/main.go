package main

import (
	"encoding/binary"
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
		ttsFlag              string
		ttsVoiceFlag         string
		ttsG2PRootFlag       string
		ttsOutDirFlag        string
	)

	flag.StringVar(&audioFlag, "audio", "", "Path to .wav audio file to transcribe in-process (STT)")
	flag.BoolVar(&streamFlag, "stream", false, "Demonstrate streaming API (NewStream) in-process with chunked audio ingestion")
	flag.StringVar(&langFlag, "language", "en", "STT model language")
	flag.StringVar(&archFlag, "arch", "tiny", "STT model architecture (e.g. tiny, tiny-streaming)")
	flag.BoolVar(&wordTimestampsFlag, "word-timestamps", false, "Enable per-word timestamps")
	flag.BoolVar(&identifySpeakersFlag, "identify-speakers", false, "Enable speaker diarization")

	flag.StringVar(&ttsFlag, "tts", "", "Text to synthesize in-process comparing one-shot vs streaming TTFA (TTS)")
	flag.StringVar(&ttsVoiceFlag, "tts-voice", "kokoro_af_heart", "TTS voice identifier (e.g. kokoro_af_heart, piper_en_US-amy-low)")
	flag.StringVar(&ttsG2PRootFlag, "tts-g2p-root", "", "Path to TTS models/voices directory (default: MOONSHINE_TTS_ROOT or cache)")
	flag.StringVar(&ttsOutDirFlag, "tts-out-dir", ".", "Directory to write output WAV files (tts_oneshot.wav and tts_streaming.wav)")
	flag.Parse()

	if audioFlag == "" && ttsFlag == "" {
		fmt.Fprintf(os.Stderr, "Usage of go-embedded (in-process STT & TTS via pkg/moonshine):\n")
		fmt.Fprintf(os.Stderr, "  STT: go run . -audio <path_to_wav> [flags]\n")
		fmt.Fprintf(os.Stderr, "  TTS: go run . -tts \"Text to speak\" [flags]\n\n")
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

	// 2. Run STT if requested
	if audioFlag != "" {
		runSTTInProcess(audioFlag, langFlag, archFlag, streamFlag, wordTimestampsFlag, identifySpeakersFlag)
	}

	// 3. Run TTS if requested
	if ttsFlag != "" {
		runTTSInProcess(ttsFlag, ttsVoiceFlag, ttsG2PRootFlag, ttsOutDirFlag)
	}
}

func runSTTInProcess(audioPath, lang, arch string, stream, wordTimestamps, identifySpeakers bool) {
	modelDir := resolveModelDir(lang, arch)
	if modelDir == "" {
		fmt.Fprintf(os.Stderr, "Error: STT model for %s/%s not found.\n", lang, arch)
		fmt.Fprintf(os.Stderr, "Run 'moonshine setup --language %s --arch %s' first.\n", lang, arch)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "[go-embedded] Loading STT model from %s...\n", modelDir)
	var opts []moonshine.Option
	if identifySpeakers {
		opts = append(opts, moonshine.Option{Name: "identify_speakers", Value: "1"})
	}

	archID := moonshine.ModelArchTiny
	if arch == "tiny-streaming" {
		archID = moonshine.ModelArchTinyStreaming
	} else if arch == "base" {
		archID = moonshine.ModelArchBase
	}

	tr, err := moonshine.LoadTranscriber(modelDir, archID, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading transcriber: %v\n", err)
		os.Exit(1)
	}
	defer tr.Close()

	samples, sampleRate, err := loadWAVSamples(audioPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading WAV file %s: %v\n", audioPath, err)
		os.Exit(1)
	}

	audioDurationSec := float64(len(samples)) / float64(sampleRate)
	fmt.Fprintf(os.Stderr, "[go-embedded] Loaded %.2fs audio (sample rate %dHz)\n", audioDurationSec, sampleRate)

	if stream {
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

func runTTSInProcess(text, voice, g2pRoot, outDir string) {
	fmt.Fprintf(os.Stderr, "\n[go-embedded] Running in-process TTS synthesis...\n")
	if g2pRoot == "" {
		g2pRoot = resolveTTSModelDir()
	}
	if g2pRoot == "" {
		fmt.Fprintf(os.Stderr, "Error: TTS model root (g2p_root) not found.\n")
		fmt.Fprintf(os.Stderr, "Specify -tts-g2p-root or set MOONSHINE_TTS_ROOT / MOONSHINE_SRC.\n")
		os.Exit(1)
	}

	var opts []moonshine.Option
	opts = append(opts, moonshine.Option{Name: "g2p_root", Value: g2pRoot})
	if voice != "" {
		opts = append(opts, moonshine.Option{Name: "voice", Value: voice})
	} else {
		opts = append(opts, moonshine.Option{Name: "voice", Value: "kokoro_af_heart"})
		voice = "kokoro_af_heart"
	}

	synth, err := moonshine.NewSynthesizer("en_us", opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating TTS synthesizer: %v\n", err)
		os.Exit(1)
	}
	defer synth.Close()

	if outDir == "" {
		outDir = "."
	}

	// 1. One-shot synthesis
	fmt.Fprintf(os.Stderr, "[go-embedded] Synthesizing one-shot...\n")
	tOneShotStart := time.Now()
	oneShotAudio, err := synth.Synthesize(text)
	oneShotTTFA := time.Since(tOneShotStart)
	oneShotTotal := oneShotTTFA
	if err != nil {
		fmt.Fprintf(os.Stderr, "One-shot TTS error: %v\n", err)
		os.Exit(1)
	}

	oneShotWAV := filepath.Join(outDir, "tts_oneshot.wav")
	if err := saveWAVFile(oneShotWAV, oneShotAudio.Samples, oneShotAudio.SampleRate); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving one-shot WAV: %v\n", err)
	}

	oneShotDuration := float64(len(oneShotAudio.Samples)) / float64(oneShotAudio.SampleRate)

	// 2. Streaming synthesis (NewStream)
	fmt.Fprintf(os.Stderr, "[go-embedded] Synthesizing streaming (NewStream)...\n")
	stream := synth.NewStream()

	tStreamStart := time.Now()
	if err := stream.PushText(text); err != nil {
		fmt.Fprintf(os.Stderr, "Error pushing text to stream: %v\n", err)
		os.Exit(1)
	}
	if err := stream.EndInput(); err != nil {
		fmt.Fprintf(os.Stderr, "Error ending stream input: %v\n", err)
		os.Exit(1)
	}

	var streamSamples []float32
	var streamSampleRate int32
	var streamTTFA time.Duration
	chunkCount := 0

	for {
		chunk, err := synth.NextChunk()
		if err == moonshine.ErrEndOfStream || err == moonshine.ErrNeedText {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error retrieving stream chunk: %v\n", err)
			break
		}
		if chunkCount == 0 {
			streamTTFA = time.Since(tStreamStart)
		}
		chunkCount++
		streamSamples = append(streamSamples, chunk.Audio.Samples...)
		streamSampleRate = chunk.Audio.SampleRate
	}
	streamTotal := time.Since(tStreamStart)

	streamWAV := filepath.Join(outDir, "tts_streaming.wav")
	if err := saveWAVFile(streamWAV, streamSamples, streamSampleRate); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving streaming WAV: %v\n", err)
	}

	streamDuration := float64(len(streamSamples)) / float64(streamSampleRate)

	speedup := 1.0
	if streamTTFA > 0 {
		speedup = float64(oneShotTTFA) / float64(streamTTFA)
	}

	fmt.Println("\n=== In-Process TTS Synthesis (One-Shot vs Streaming TTFA) ===")
	fmt.Printf("Text:   %q\n", text)
	fmt.Printf("Voice:  %s (%dHz)\n\n", voice, oneShotAudio.SampleRate)
	fmt.Printf("%-24s %-18s %-24s %-14s\n", "Metric", "One-Shot", "Streaming (NewStream)", "Speedup")
	fmt.Println("------------------------------------------------------------------------------------")
	fmt.Printf("%-24s %-18s %-24s %-14s\n",
		"Time-to-First-Audio",
		fmt.Sprintf("%dms", oneShotTTFA.Milliseconds()),
		fmt.Sprintf("%dms", streamTTFA.Milliseconds()),
		fmt.Sprintf("%.1fx faster", speedup),
	)
	fmt.Printf("%-24s %-18s %-24s %-14s\n",
		"Total Generation Time",
		fmt.Sprintf("%dms", oneShotTotal.Milliseconds()),
		fmt.Sprintf("%dms", streamTotal.Milliseconds()),
		"-",
	)
	fmt.Printf("%-24s %-18s %-24s %-14s\n",
		"Audio Duration",
		fmt.Sprintf("%.2fs (%d smp)", oneShotDuration, len(oneShotAudio.Samples)),
		fmt.Sprintf("%.2fs (%d smp)", streamDuration, len(streamSamples)),
		"-",
	)
	fmt.Printf("%-24s %-18s %-24s %-14s\n",
		"Saved WAV File",
		oneShotWAV,
		streamWAV,
		"-",
	)
	fmt.Println()
}

func resolveTTSModelDir() string {
	if root := os.Getenv("MOONSHINE_TTS_ROOT"); root != "" {
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			return root
		}
	}
	if root := os.Getenv("MOONSHINE_SMOKE_TTS_ROOT"); root != "" {
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			return root
		}
	}
	if src := os.Getenv("MOONSHINE_SRC"); src != "" {
		candidate := filepath.Join(src, "core", "moonshine-tts", "data")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidates := []string{
			filepath.Join(home, "projects", "github", "moonshine", "core", "moonshine-tts", "data"),
			filepath.Join(home, "Library", "Caches", "moonshine_voice", "tts"),
			filepath.Join(home, ".cache", "moonshine_voice", "tts"),
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && fi.IsDir() {
				return c
			}
		}
	}
	return ""
}

func saveWAVFile(path string, samples []float32, sampleRate int32) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	numChannels := uint16(1)
	bitsPerSample := uint16(16)
	bytesPerSample := bitsPerSample / 8
	dataSize := uint32(len(samples)) * uint32(bytesPerSample) * uint32(numChannels)
	fileSize := 36 + dataSize

	// RIFF header
	if _, err := f.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, fileSize); err != nil {
		return err
	}
	if _, err := f.Write([]byte("WAVEfmt ")); err != nil {
		return err
	}

	// Subchunk 1 ("fmt ")
	subchunk1Size := uint32(16)
	audioFormat := uint16(1) // PCM
	byteRate := uint32(sampleRate) * uint32(numChannels) * uint32(bytesPerSample)
	blockAlign := numChannels * bytesPerSample

	if err := binary.Write(f, binary.LittleEndian, subchunk1Size); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, audioFormat); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, numChannels); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, byteRate); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, blockAlign); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, bitsPerSample); err != nil {
		return err
	}

	// Subchunk 2 ("data")
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, dataSize); err != nil {
		return err
	}

	for _, s := range samples {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		val := int16(s * 32767.0)
		if err := binary.Write(f, binary.LittleEndian, val); err != nil {
			return err
		}
	}
	return nil
}
