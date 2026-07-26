package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFilePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("skipping test without home dir")
	}

	economistPath := filepath.Join(home, "Downloads", "economist.wav")
	if _, err := os.Stat(economistPath); os.IsNotExist(err) {
		t.Skip("economist.wav not present in Downloads")
	}

	resolved := resolveFilePath("economist.wav")
	if resolved != economistPath {
		t.Errorf("expected %s, got %s", economistPath, resolved)
	}

	samples, sampleRate, err := loadAudioSamples("economist.wav")
	if err != nil {
		t.Fatalf("failed to load audio samples for economist.wav: %v", err)
	}

	if len(samples) == 0 {
		t.Errorf("expected non-empty samples")
	}
	if sampleRate <= 0 {
		t.Errorf("invalid sample rate: %d", sampleRate)
	}
	t.Logf("Successfully loaded economist.wav: %d samples, sample rate: %d Hz", len(samples), sampleRate)
}
