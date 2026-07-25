package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
)

func TestExpandTranscribeInputs_LocalFilesAndDirs(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "audio1.wav")
	file2 := filepath.Join(tmpDir, "audio2.wav")
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file3 := filepath.Join(subDir, "audio3.wav")

	for _, f := range []string{file1, file2, file3} {
		if err := os.WriteFile(f, []byte("fake wav"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	ctx := context.Background()

	// 1. Directory scan recursive
	inputs, err := expandTranscribeInputs(ctx, []string{tmpDir}, true)
	if err != nil {
		t.Fatalf("expand recursive dir: %v", err)
	}
	if len(inputs) != 3 {
		t.Errorf("len(inputs) = %d, want 3", len(inputs))
	}

	// 2. Directory scan non-recursive
	inputsNonRec, err := expandTranscribeInputs(ctx, []string{tmpDir}, false)
	if err != nil {
		t.Fatalf("expand non-recursive dir: %v", err)
	}
	if len(inputsNonRec) != 2 {
		t.Errorf("len(inputsNonRec) = %d, want 2", len(inputsNonRec))
	}

	// 3. Glob match
	globPattern := filepath.Join(tmpDir, "*.wav")
	inputsGlob, err := expandTranscribeInputs(ctx, []string{globPattern}, true)
	if err != nil {
		t.Fatalf("expand glob: %v", err)
	}
	if len(inputsGlob) != 2 {
		t.Errorf("len(inputsGlob) = %d, want 2", len(inputsGlob))
	}
}

func TestBuildBatchManifest_AggregatesResultsAndErrors(t *testing.T) {
	inputs := []string{"f1.wav", "f2.wav", "f3.wav"}
	results := []batchFileResult{
		{
			Input:  "f1.wav",
			Status: "ok",
			Stats: batchFileStats{
				AudioDurationSec: 10.0,
				InferenceMs:      500.0,
				MeanConfidence:   0.90,
			},
			Lines: []moonshine.Line{
				{Text: "line 1", Confidence: 0.90},
			},
		},
		{
			Input:  "f2.wav",
			Status: "failed",
			Error:  "corrupt wav header",
		},
		{
			Input:  "f3.wav",
			Status: "ok",
			Stats: batchFileStats{
				AudioDurationSec: 20.0,
				InferenceMs:      1000.0,
				MeanConfidence:   0.96,
			},
			Lines: []moonshine.Line{
				{Text: "line 2", Confidence: 0.96},
			},
		},
	}

	manifest := buildBatchManifest(inputs, results)

	if manifest.Version != "1.0" {
		t.Errorf("version = %q, want '1.0'", manifest.Version)
	}
	if manifest.Summary.TotalFiles != 3 {
		t.Errorf("total_files = %d, want 3", manifest.Summary.TotalFiles)
	}
	if manifest.Summary.SuccessfulFiles != 2 {
		t.Errorf("successful_files = %d, want 2", manifest.Summary.SuccessfulFiles)
	}
	if manifest.Summary.FailedFiles != 1 {
		t.Errorf("failed_files = %d, want 1", manifest.Summary.FailedFiles)
	}
	if manifest.Summary.TotalAudioSec != 30.0 {
		t.Errorf("total_audio_sec = %f, want 30.0", manifest.Summary.TotalAudioSec)
	}
	if manifest.Summary.TotalInferenceMs != 1500.0 {
		t.Errorf("total_inference_ms = %f, want 1500.0", manifest.Summary.TotalInferenceMs)
	}
	if manifest.Summary.ConfidenceSummary.Min != 0.90 || manifest.Summary.ConfidenceSummary.Max != 0.96 {
		t.Errorf("confidence min/max = (%f, %f), want (0.90, 0.96)",
			manifest.Summary.ConfidenceSummary.Min, manifest.Summary.ConfidenceSummary.Max)
	}
}
