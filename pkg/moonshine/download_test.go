package moonshine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDependencyGroup_UnmarshalJSON_LegacyShape(t *testing.T) {
	rawJSON := `{
		"base_url": "https://download.moonshine.ai/model/tiny-en",
		"files": [
			"encoder_model.ort",
			"decoder_model_merged.ort",
			"tokenizer.bin"
		]
	}`

	var group DependencyGroup
	if err := json.Unmarshal([]byte(rawJSON), &group); err != nil {
		t.Fatalf("failed to unmarshal legacy DependencyGroup JSON: %v", err)
	}

	if group.BaseURL != "https://download.moonshine.ai/model/tiny-en" {
		t.Errorf("unexpected BaseURL: %q", group.BaseURL)
	}

	expectedFiles := []string{"encoder_model.ort", "decoder_model_merged.ort", "tokenizer.bin"}
	if len(group.Files) != len(expectedFiles) {
		t.Fatalf("expected %d files, got %d", len(expectedFiles), len(group.Files))
	}

	for i, name := range expectedFiles {
		if group.Files[i].Name != name {
			t.Errorf("file[%d] name: expected %q, got %q", i, name, group.Files[i].Name)
		}
		if group.Files[i].URL != "" {
			t.Errorf("file[%d] URL: expected empty, got %q", i, group.Files[i].URL)
		}
	}
}

func TestDependencyGroup_UnmarshalJSON_ObjectShape(t *testing.T) {
	rawJSON := `{
		"base_url": "https://download.moonshine.ai/model/tiny-en",
		"files": [
			{
				"name": "encoder_model.ort",
				"url": "https://cdn.example.com/encoder_model.ort",
				"size": 123456,
				"checksum": "abc123==",
				"checksum_type": "crc32c"
			},
			{
				"name": "tokenizer.bin",
				"url": "https://cdn.example.com/tokenizer.bin",
				"size": 789,
				"checksum": "def456==",
				"checksum_type": "crc32c"
			}
		]
	}`

	var group DependencyGroup
	if err := json.Unmarshal([]byte(rawJSON), &group); err != nil {
		t.Fatalf("failed to unmarshal object DependencyGroup JSON: %v", err)
	}

	if group.BaseURL != "https://download.moonshine.ai/model/tiny-en" {
		t.Errorf("unexpected BaseURL: %q", group.BaseURL)
	}

	if len(group.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(group.Files))
	}

	f0 := group.Files[0]
	if f0.Name != "encoder_model.ort" {
		t.Errorf("f0.Name: expected encoder_model.ort, got %q", f0.Name)
	}
	if f0.URL != "https://cdn.example.com/encoder_model.ort" {
		t.Errorf("f0.URL: expected https://cdn.example.com/encoder_model.ort, got %q", f0.URL)
	}
	if f0.Size != 123456 {
		t.Errorf("f0.Size: expected 123456, got %d", f0.Size)
	}
	if f0.Checksum != "abc123==" {
		t.Errorf("f0.Checksum: expected abc123==, got %q", f0.Checksum)
	}
	if f0.ChecksumType != "crc32c" {
		t.Errorf("f0.ChecksumType: expected crc32c, got %q", f0.ChecksumType)
	}
}

func TestDependencyGroup_UnmarshalJSON_EmptyFiles(t *testing.T) {
	rawJSON := `{
		"base_url": "https://download.moonshine.ai/model/empty",
		"files": []
	}`

	var group DependencyGroup
	if err := json.Unmarshal([]byte(rawJSON), &group); err != nil {
		t.Fatalf("failed to unmarshal empty files DependencyGroup JSON: %v", err)
	}

	if len(group.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(group.Files))
	}
}

func TestDownload_MockServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/legacy/encoder.ort":
			_, _ = w.Write([]byte("legacy-encoder-data"))
		case "/custom/tokenizer.bin":
			_, _ = w.Write([]byte("custom-tokenizer-data"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	manifest := DependencyManifest{
		Groups: []DependencyGroup{
			{
				BaseURL: ts.URL + "/legacy",
				Files: []DependencyFile{
					{Name: "encoder.ort"}, // Legacy: URL inferred from BaseURL + Name
				},
			},
			{
				BaseURL: ts.URL + "/dummy",
				Files: []DependencyFile{
					{Name: "tokenizer.bin", URL: ts.URL + "/custom/tokenizer.bin"}, // Object: explicit URL
				},
			},
		},
	}

	tmpDir, err := os.MkdirTemp("", "moonshine-download-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := Download(context.Background(), manifest, tmpDir, false); err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	// Verify legacy file
	legacyPath := filepath.Join(tmpDir, stripScheme(ts.URL+"/legacy"), "encoder.ort")
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("failed to read legacy downloaded file: %v", err)
	}
	if string(data) != "legacy-encoder-data" {
		t.Errorf("legacy content mismatch: got %q", string(data))
	}

	// Verify custom URL file
	customPath := filepath.Join(tmpDir, stripScheme(ts.URL+"/dummy"), "tokenizer.bin")
	data2, err := os.ReadFile(customPath)
	if err != nil {
		t.Fatalf("failed to read custom URL downloaded file: %v", err)
	}
	if string(data2) != "custom-tokenizer-data" {
		t.Errorf("custom URL content mismatch: got %q", string(data2))
	}
}

func TestGroupDirAndPrimaryModelDir(t *testing.T) {
	group := DependencyGroup{
		BaseURL: "https://download.moonshine.ai/model/tiny-en",
	}

	dir := GroupDir("/cache", group)
	expected := filepath.Join("/cache", "download.moonshine.ai", "model", "tiny-en")
	if dir != expected {
		t.Errorf("GroupDir: expected %q, got %q", expected, dir)
	}

	manifest := DependencyManifest{
		Groups: []DependencyGroup{group},
	}

	primaryDir, err := PrimaryModelDir("/cache", manifest)
	if err != nil {
		t.Fatalf("PrimaryModelDir error: %v", err)
	}
	if primaryDir != expected {
		t.Errorf("PrimaryModelDir: expected %q, got %q", expected, primaryDir)
	}

	_, err = PrimaryModelDir("/cache", DependencyManifest{})
	if err == nil {
		t.Errorf("expected error for empty manifest in PrimaryModelDir")
	}
}
