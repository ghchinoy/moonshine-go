package main

import (
	"fmt"
	"os"
	"path/filepath"
)

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
		return nil, 0, fmt.Errorf("unsupported bitsPerSample %d", bytesPerSample)
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
