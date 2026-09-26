package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
	"github.com/spf13/viper"
)

func TestModelArchFromFlag(t *testing.T) {
	tests := []struct {
		input   string
		want    uint32
		wantErr bool
	}{
		{"tiny", moonshine.ModelArchTiny, false},
		{"base", moonshine.ModelArchBase, false},
		{"tiny-streaming", moonshine.ModelArchTinyStreaming, false},
		{"base-streaming", moonshine.ModelArchBaseStreaming, false},
		{"small-streaming", moonshine.ModelArchSmallStreaming, false},
		{"medium-streaming", moonshine.ModelArchMediumStreaming, false},
		{"none", 0, false},
		{"unknown-arch", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		got, err := modelArchFromFlag(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("modelArchFromFlag(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("modelArchFromFlag(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestTTSCacheDir(t *testing.T) {
	tempDir := t.TempDir()
	origModelDir := viper.GetString("model.dir")
	viper.Set("model.dir", tempDir)
	defer viper.Set("model.dir", origModelDir)

	expected := filepath.Join(tempDir, "download.moonshine.ai", "tts")
	got := ttsCacheDir()
	if got != expected {
		t.Errorf("ttsCacheDir() = %q, want %q", got, expected)
	}
}

func TestG2PRootFallbackPrecedence(t *testing.T) {
	tempModelDir := t.TempDir()
	origModelDir := viper.GetString("model.dir")
	origG2PRoot := viper.GetString("tts.g2p_root")
	origSrcDir := viper.GetString("moonshine.src_dir")

	defer func() {
		viper.Set("model.dir", origModelDir)
		viper.Set("tts.g2p_root", origG2PRoot)
		viper.Set("moonshine.src_dir", origSrcDir)
	}()

	viper.Set("model.dir", tempModelDir)

	// Case 1: Neither src_dir nor downloaded voices exist -> resolveG2PRoot remains empty
	viper.Set("tts.g2p_root", "")
	viper.Set("moonshine.src_dir", "")
	expectedDownloaded := filepath.Join(tempModelDir, "download.moonshine.ai", "tts")

	if val := resolveG2PRoot(); val != "" {
		t.Errorf("expected empty resolveG2PRoot when neither src_dir nor downloaded exists, got %q", val)
	}

	// Case 2: Downloaded directory exists, src_dir unset -> defaults to downloadedDir
	if err := os.MkdirAll(expectedDownloaded, 0o755); err != nil {
		t.Fatalf("creating downloadedDir: %v", err)
	}
	if val := resolveG2PRoot(); val != expectedDownloaded {
		t.Errorf("expected resolveG2PRoot = %q, got %q", expectedDownloaded, val)
	}

	// Case 3: moonshine.src_dir is set -> outranks downloaded directory
	fakeSrc := filepath.Join(t.TempDir(), "fake_src")
	viper.Set("moonshine.src_dir", fakeSrc)
	expectedSrcG2P := filepath.Join(fakeSrc, "core", "moonshine-tts", "data")

	if val := resolveG2PRoot(); val != expectedSrcG2P {
		t.Errorf("expected moonshine.src_dir to outrank downloaded dir, got %q, want %q", val, expectedSrcG2P)
	}

	// Case 4: Explicit flag or config key -> outranks everything
	viper.Set("tts.g2p_root", "/explicit/custom/path")
	if val := resolveG2PRoot(); val != "/explicit/custom/path" {
		t.Errorf("explicit tts.g2p_root should outrank everything, got %q", val)
	}
}

func TestDownloadTTSVoices_Validation(t *testing.T) {
	// Empty voice list
	err := downloadTTSVoices(context.Background(), "en_us", "", t.TempDir(), false)
	if err == nil {
		t.Error("downloadTTSVoices(empty voice) = nil, want error")
	}

	// ZipVoice rejection
	err = downloadTTSVoices(context.Background(), "en_us", "zipvoice_american_female", t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "ZipVoice") {
		t.Errorf("downloadTTSVoices(zipvoice) = %v, want error explaining ZipVoice", err)
	}
}
