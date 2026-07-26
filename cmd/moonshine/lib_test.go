package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
)

func TestResolveAudioRecordPath(t *testing.T) {
	// 1. Empty target -> default filename
	gotDefault := resolveAudioRecordPath("")
	if !strings.HasPrefix(gotDefault, "moonshine_clip_") || !strings.HasSuffix(gotDefault, "_16k_mono.wav") {
		t.Errorf("resolveAudioRecordPath(\"\") = %q, want moonshine_clip_..._16k_mono.wav", gotDefault)
	}

	// 2. Directory target -> default filename inside directory
	tmpDir := t.TempDir()
	gotDir := resolveAudioRecordPath(tmpDir)
	if !strings.HasPrefix(filepath.Base(gotDir), "moonshine_clip_") {
		t.Errorf("resolveAudioRecordPath(dir) = %q, want filename inside %s", gotDir, tmpDir)
	}

	// 3. Explicit filename target -> preserved
	explicit := filepath.Join(tmpDir, "my_custom_ref.wav")
	gotExplicit := resolveAudioRecordPath(explicit)
	if gotExplicit != explicit {
		t.Errorf("resolveAudioRecordPath(%q) = %q, want %q", explicit, gotExplicit, explicit)
	}
}

func TestSaveLineAudio(t *testing.T) {
	tmpDir := t.TempDir()
	line := moonshine.Line{
		ID:        42,
		StartTime: 1.25,
		Text:      "Test voice recording clip",
		AudioData: []float32{0.1, 0.2, -0.1, -0.2},
	}

	if err := saveLineAudio(tmpDir, line); err != nil {
		t.Fatalf("saveLineAudio: %v", err)
	}

	wavPath := filepath.Join(tmpDir, "line_42_001250ms_16k.wav")
	txtPath := filepath.Join(tmpDir, "line_42_001250ms_16k.txt")

	if _, err := os.Stat(wavPath); os.IsNotExist(err) {
		t.Errorf("expected wav file %s does not exist", wavPath)
	}

	txtBytes, err := os.ReadFile(txtPath)
	if err != nil {
		t.Fatalf("reading txt file %s: %v", txtPath, err)
	}
	if string(txtBytes) != "Test voice recording clip" {
		t.Errorf("txt content = %q, want %q", string(txtBytes), "Test voice recording clip")
	}
}
