package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
)

func TestCheckModelDownload_ForeignLanguageIsolation(t *testing.T) {
	// Create temporary model root dir with English models
	tmpDir := t.TempDir()

	tinyEnDir := filepath.Join(tmpDir, "download.moonshine.ai", "model", "tiny-en", "quantized", "tiny-en")
	if err := os.MkdirAll(tinyEnDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tinyEnDir, "encoder_model.ort"), []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Language "de" checking "tiny": should NOT match tiny-en
	statusDE := modelStatus{
		Name:      "tiny",
		ArchID:    moonshine.ModelArchTiny,
		Streaming: false,
	}
	checkModelDownload("de", tmpDir, &statusDE)
	if statusDE.Downloaded {
		t.Errorf("expected statusDE.Downloaded to be false for 'de' with only English models, got true (%s)", statusDE.Path)
	}

	// 2. Language "en" checking "tiny": SHOULD match tiny-en
	statusEN := modelStatus{
		Name:      "tiny",
		ArchID:    moonshine.ModelArchTiny,
		Streaming: false,
	}
	checkModelDownload("en", tmpDir, &statusEN)
	if !statusEN.Downloaded {
		t.Errorf("expected statusEN.Downloaded to be true for 'en', got false")
	}

	// 3. Language "tl" checking "base": should NOT match base-en
	baseEnDir := filepath.Join(tmpDir, "download.moonshine.ai", "model", "base-en", "quantized", "base-en")
	if err := os.MkdirAll(baseEnDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseEnDir, "encoder_model.ort"), []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}

	statusTL := modelStatus{
		Name:      "base",
		ArchID:    moonshine.ModelArchBase,
		Streaming: false,
	}
	checkModelDownload("tl", tmpDir, &statusTL)
	if statusTL.Downloaded {
		t.Errorf("expected statusTL.Downloaded to be false for 'tl' with only English models, got true (%s)", statusTL.Path)
	}
}
