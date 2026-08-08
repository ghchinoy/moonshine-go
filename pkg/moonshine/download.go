package moonshine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

// DependencyFile represents a single downloadable model file in a dependency
// group, as returned by moonshine_get_stt_dependencies /
// moonshine_get_intent_dependencies in upstream v0.1.0+.
type DependencyFile struct {
	Name         string `json:"name"`
	URL          string `json:"url,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Checksum     string `json:"checksum,omitempty"`
	ChecksumType string `json:"checksum_type,omitempty"`
}

// DependencyGroup is one downloadable group of model files sharing a base
// URL, as returned by moonshine_get_stt_dependencies /
// moonshine_get_intent_dependencies.
type DependencyGroup struct {
	BaseURL string           `json:"base_url"`
	Files   []DependencyFile `json:"files"`
}

// UnmarshalJSON implements custom JSON unmarshaling for DependencyGroup to
// support both the legacy string-array shape (["encoder_model.ort"]) from
// v0.0.73 and earlier, and the object-array shape ([{"name": "..."}]) from
// v0.1.0+.
func (g *DependencyGroup) UnmarshalJSON(data []byte) error {
	type rawGroup struct {
		BaseURL string          `json:"base_url"`
		Files   json.RawMessage `json:"files"`
	}
	var raw rawGroup
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	g.BaseURL = raw.BaseURL
	if len(raw.Files) == 0 {
		return nil
	}

	// Try parsing files as []DependencyFile (v0.1.0+ object shape)
	var fileObjs []DependencyFile
	if err := json.Unmarshal(raw.Files, &fileObjs); err == nil && (len(fileObjs) == 0 || fileObjs[0].Name != "") {
		g.Files = fileObjs
		return nil
	}

	// Fall back to parsing files as []string (legacy string shape)
	var fileStrs []string
	if err := json.Unmarshal(raw.Files, &fileStrs); err == nil {
		g.Files = make([]DependencyFile, len(fileStrs))
		for i, s := range fileStrs {
			g.Files[i] = DependencyFile{Name: s}
		}
		return nil
	}

	return fmt.Errorf("moonshine: files field in dependency group must be an array of objects or strings")
}

// DependencyManifest is the download manifest for a model.
type DependencyManifest struct {
	Groups []DependencyGroup `json:"groups"`
}

// GetSTTDependencies returns the download manifest for a speech-to-text
// model (moonshine_get_stt_dependencies). language is a language code (e.g.
// "en") or English name. Recognized opts: "model_arch" (one of the
// ModelArch* constants as a decimal string), "include_spelling" ("true"/"false").
func GetSTTDependencies(language string, opts ...Option) (DependencyManifest, error) {
	if !Loaded() {
		return DependencyManifest{}, errNotLoaded
	}
	cOpts, optCount, keep := toCOptions(opts)
	var outPtr unsafe.Pointer
	code := fnGetSTTDependencies(language, cOpts, optCount, &outPtr)
	runtime.KeepAlive(keep)
	if err := checkCode("get_stt_dependencies", code); err != nil {
		return DependencyManifest{}, err
	}
	defer freeC(outPtr)
	raw := goString((*byte)(outPtr))
	var manifest DependencyManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return DependencyManifest{}, fmt.Errorf("moonshine: parsing stt dependency manifest: %w", err)
	}
	return manifest, nil
}

// GetDiarizationDependencies returns the download manifest for speaker
// diarization models (moonshine_get_diarization_dependencies). The manifest
// contains segmentation.ort and embedding.ort under the diarization CDN directory.
func GetDiarizationDependencies() (DependencyManifest, error) {
	if !Loaded() {
		return DependencyManifest{}, errNotLoaded
	}
	if fnGetDiarizationDependencies == nil {
		return DependencyManifest{}, fmt.Errorf("moonshine: moonshine_get_diarization_dependencies symbol not found in loaded library")
	}
	var outPtr unsafe.Pointer
	code := fnGetDiarizationDependencies(&outPtr)
	if err := checkCode("get_diarization_dependencies", code); err != nil {
		return DependencyManifest{}, err
	}
	defer freeC(outPtr)
	raw := goString((*byte)(outPtr))
	var manifest DependencyManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return DependencyManifest{}, fmt.Errorf("moonshine: parsing diarization dependency manifest: %w", err)
	}
	return manifest, nil
}

// GetTTSDependencies returns the download manifest for TTS + G2P assets
// (moonshine_get_tts_dependencies).
func GetTTSDependencies(languages string, opts ...Option) (DependencyManifest, error) {
	if !Loaded() {
		return DependencyManifest{}, errNotLoaded
	}
	cOpts, optCount, keep := toCOptions(opts)
	var outPtr unsafe.Pointer
	code := fnGetTTSDependencies(languages, cOpts, optCount, &outPtr)
	runtime.KeepAlive(keep)
	if err := checkCode("get_tts_dependencies", code); err != nil {
		return DependencyManifest{}, err
	}
	defer freeC(outPtr)
	raw := goString((*byte)(outPtr))
	var manifest DependencyManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return DependencyManifest{}, fmt.Errorf("moonshine: parsing tts dependency manifest: %w", err)
	}
	return manifest, nil
}

// GetTTSDependencyKeys returns the canonical asset key names (relative
// paths under g2p_root, e.g. "kokoro/model.onnx") needed for TTS + G2P for
// the given language(s) (moonshine_get_tts_dependencies). Supports both
// v0.1.1+ DependencyManifest JSON object shape and legacy flat string array shape.
func GetTTSDependencyKeys(languages string, opts ...Option) ([]string, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	cOpts, optCount, keep := toCOptions(opts)
	var outPtr unsafe.Pointer
	code := fnGetTTSDependencies(languages, cOpts, optCount, &outPtr)
	runtime.KeepAlive(keep)
	if err := checkCode("get_tts_dependencies", code); err != nil {
		return nil, err
	}
	defer freeC(outPtr)
	raw := goString((*byte)(outPtr))

	// Try object shape first (v0.1.1+)
	var manifest DependencyManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err == nil && len(manifest.Groups) > 0 {
		var keys []string
		for _, group := range manifest.Groups {
			for _, file := range group.Files {
				if file.Name != "" {
					keys = append(keys, file.Name)
				}
			}
		}
		return keys, nil
	}

	// Fallback to legacy flat string array shape
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("moonshine: parsing tts dependency keys: %w", err)
	}
	return keys, nil
}

// DiarizationModelDir returns the cache directory path where diarization models
// (segmentation.ort and embedding.ort) are downloaded under root.
func DiarizationModelDir(root string) (string, error) {
	manifest, err := GetDiarizationDependencies()
	if err != nil {
		return "", err
	}
	return PrimaryModelDir(root, manifest)
}

// GroupDir returns the directory a group's files are downloaded into under
// root: root joined with the group's BaseURL with its scheme stripped (e.g.
// "download.moonshine.ai/model/tiny-en/quantized/tiny-en"). This mirrors the
// layout moonshine's own Python package uses for its cache
// (get_cache_dir()/<url-without-scheme>), so pointing root at the same cache
// directory (see MOONSHINE_VOICE_CACHE) lets both share downloaded models.
// Namespacing by the full URL path -- rather than dumping every model's
// files into one flat directory -- also avoids filename collisions: STT
// models for different architectures/languages all use the same file names
// (encoder_model.ort, decoder_model_merged.ort, tokenizer.bin, ...).
func GroupDir(root string, g DependencyGroup) string {
	return filepath.Join(root, stripScheme(g.BaseURL))
}

// PrimaryModelDir returns GroupDir for the manifest's first group -- the
// directory to pass to LoadTranscriber for a manifest with no
// "include_spelling" extra group. Returns an error if the manifest is empty.
func PrimaryModelDir(root string, manifest DependencyManifest) (string, error) {
	if len(manifest.Groups) == 0 {
		return "", fmt.Errorf("moonshine: dependency manifest has no groups")
	}
	return GroupDir(root, manifest.Groups[0]), nil
}

func stripScheme(rawURL string) string {
	var s string
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		s = u.Hostname() + u.Path
	} else {
		s = strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
	}
	s = strings.ReplaceAll(s, ":", "_")
	return strings.TrimPrefix(s, "/")
}

// Download fetches every file in the manifest into root, namespacing each
// group under GroupDir(root, group) to avoid cross-model filename
// collisions (see GroupDir). Skips files that already exist unless force is
// true.
func Download(ctx context.Context, manifest DependencyManifest, root string, force bool) error {
	for _, group := range manifest.Groups {
		destDir := GroupDir(root, group)
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return fmt.Errorf("moonshine: creating %s: %w", destDir, err)
		}
		for _, file := range group.Files {
			dest := filepath.Join(destDir, file.Name)
			if !force {
				if fi, err := os.Stat(dest); err == nil && !fi.IsDir() && fi.Size() > 0 {
					continue
				}
			}
			fileURL := file.URL
			if fileURL == "" {
				fileURL = group.BaseURL + "/" + file.Name
			}
			if err := downloadFile(ctx, fileURL, dest); err != nil {
				return fmt.Errorf("moonshine: downloading %s: %w", fileURL, err)
			}
		}
	}
	return nil
}

func downloadFile(ctx context.Context, fileURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
