package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/go-mp3"
)

// resolveModelDir resolves the STT model directory under $MOONSHINE_VOICE_CACHE
// or standard OS cache paths.
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

func resolveFilePath(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if !filepath.IsAbs(path) {
		home, err := os.UserHomeDir()
		if err == nil {
			candidates := []string{
				filepath.Join(home, "Downloads", path),
				filepath.Join(home, "Desktop", path),
				filepath.Join(home, path),
			}
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					return c
				}
			}
		}
	}
	return path
}

func loadMP3Samples(path string) ([]float32, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	decoder, err := mp3.NewDecoder(file)
	if err != nil {
		return nil, 0, fmt.Errorf("decoding MP3 header: %w", err)
	}

	sampleRate := decoder.SampleRate()
	buf := make([]byte, 4096)
	var rawPCM []byte
	for {
		n, err := decoder.Read(buf)
		if n > 0 {
			rawPCM = append(rawPCM, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	numChannels := 2
	totalFrames := len(rawPCM) / (2 * numChannels)
	samples := make([]float32, totalFrames)

	for i := 0; i < totalFrames; i++ {
		offset := i * 2 * numChannels
		leftRaw := int16(int(rawPCM[offset]) | (int(rawPCM[offset+1]) << 8))
		rightRaw := int16(int(rawPCM[offset+2]) | (int(rawPCM[offset+3]) << 8))
		mono := (float32(leftRaw) + float32(rightRaw)) / 2.0 / 32768.0
		samples[i] = mono
	}

	return samples, sampleRate, nil
}

func loadAudioSamples(path string) ([]float32, int, error) {
	resolvedPath := resolveFilePath(path)

	ext := filepath.Ext(resolvedPath)
	if ext == ".mp3" || ext == ".MP3" {
		return loadMP3Samples(resolvedPath)
	}

	samples, rate, err := loadWAVSamples(resolvedPath)
	if err == nil {
		return samples, rate, nil
	}

	mp3Samples, mp3Rate, mp3Err := loadMP3Samples(resolvedPath)
	if mp3Err == nil {
		return mp3Samples, mp3Rate, nil
	}

	return nil, 0, fmt.Errorf("reading audio %s: %w", resolvedPath, err)
}

// loadWAVSamples reads mono 16kHz float32 or int16 WAV audio samples into []float32.
func loadWAVSamples(path string) ([]float32, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("invalid WAV file header: %s", path)
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
		return nil, 0, fmt.Errorf("data chunk not found in WAV file: %s", path)
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
