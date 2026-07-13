// Package moonshine is a pure-Go (no cgo required to build this package)
// client for libmoonshine, the C ABI shared library behind
// https://github.com/moonshine-ai/moonshine. It uses
// github.com/ebitengine/purego to dlopen the library at runtime and bind
// directly to its exported C functions -- the same integration point
// moonshine's own Python bindings use (ctypes.CDLL over moonshine-c-api.h).
//
// Build libmoonshine itself from a local moonshine checkout with
// scripts/build-libmoonshine.sh, then call Load (directly, or indirectly via
// the CLI's --lib-dir flag / MOONSHINE_LIB_DIR env var) before using any
// other function in this package.
package moonshine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// HeaderVersion mirrors MOONSHINE_HEADER_VERSION in moonshine-c-api.h. It is
// passed to every transcriber/synthesizer creation call so that a newer
// shared library can emulate the behavior this package was written against.
const HeaderVersion int32 = 20000

var (
	loadOnce  sync.Once
	loadErr   error
	libHandle uintptr
	libPath   string
)

// Loaded reports whether Load has already succeeded.
func Loaded() bool { return libHandle != 0 }

// LibPath returns the path of the loaded library, or "" if Load has not
// succeeded yet.
func LibPath() string { return libPath }

var errNotLoaded = errors.New("moonshine: Load must be called (and succeed) before using this package")

func libFileNames() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"libmoonshine.dylib"}
	case "linux":
		return []string{"libmoonshine.so"}
	default:
		return []string{"libmoonshine.dylib", "libmoonshine.so", "moonshine.dll"}
	}
}

// ResolveLibPath figures out the path to libmoonshine, trying (in order):
//
//  1. override, if non-empty -- either a direct path to the library file, or
//     a directory containing it.
//  2. $MOONSHINE_LIB_PATH -- a direct path to the library file.
//  3. $MOONSHINE_LIB_DIR -- a directory containing the library file.
//  4. ./.moonshine/lib -- the default output directory of
//     scripts/build-libmoonshine.sh, relative to the current working
//     directory.
//  5. Common OS install locations.
func ResolveLibPath(override string) (string, error) {
	var candidates []string
	if override != "" {
		candidates = append(candidates, override)
	}
	if p := os.Getenv("MOONSHINE_LIB_PATH"); p != "" {
		candidates = append(candidates, p)
	}
	if d := os.Getenv("MOONSHINE_LIB_DIR"); d != "" {
		candidates = append(candidates, d)
	}
	candidates = append(candidates,
		filepath.Join(".", ".moonshine", "lib"),
		"/usr/local/lib",
		"/opt/homebrew/lib",
	)

	for _, c := range candidates {
		if p, ok := resolveOne(c); ok {
			return p, nil
		}
	}
	return "", fmt.Errorf("moonshine: could not find libmoonshine (checked %v); build it with scripts/build-libmoonshine.sh and set MOONSHINE_LIB_DIR, or pass an explicit path", candidates)
}

func resolveOne(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	if !info.IsDir() {
		return p, true
	}
	for _, name := range libFileNames() {
		full := filepath.Join(p, name)
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			return full, true
		}
	}
	return "", false
}

// Load dlopen's libmoonshine (see ResolveLibPath for how the path is chosen)
// and binds every C API symbol this package uses. Safe to call more than
// once; only the first call takes effect and its result is cached.
func Load(path string) error {
	loadOnce.Do(func() {
		resolved, err := ResolveLibPath(path)
		if err != nil {
			loadErr = err
			return
		}
		h, err := purego.Dlopen(resolved, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("moonshine: dlopen %s: %w", resolved, err)
			return
		}
		libHandle = h
		libPath = resolved
		registerSymbols(h)
	})
	return loadErr
}

// --- bound C functions (private; see moonshine-c-api.h for documentation of
// each). Populated by registerSymbols once Load succeeds.
var (
	fnGetVersion    func() int32
	fnErrorToString func(code int32) string

	fnLoadTranscriberFromFiles   func(path string, modelArch uint32, options *cOption, optionsCount uint64, version int32) int32
	fnFreeTranscriber            func(handle int32)
	fnTranscribeWithoutStreaming func(handle int32, audioData *float32, audioLength uint64, sampleRate int32, flags uint32, outTranscript *unsafe.Pointer) int32
	fnCreateStream               func(transcriberHandle int32, flags uint32) int32
	fnFreeStream                 func(transcriberHandle, streamHandle int32) int32
	fnStartStream                func(transcriberHandle, streamHandle int32) int32
	fnStopStream                 func(transcriberHandle, streamHandle int32) int32
	fnAddAudioToStream           func(transcriberHandle, streamHandle int32, newAudioData *float32, audioLength uint64, sampleRate int32, flags uint32) int32
	fnTranscribeStream           func(transcriberHandle, streamHandle int32, flags uint32, outTranscript *unsafe.Pointer) int32

	fnCreateTTSSynthesizerFromFiles func(language string, filenames unsafe.Pointer, filenamesCount uint64, options *cOption, optionsCount uint64, version int32) int32
	fnFreeTTSSynthesizer            func(handle int32)
	fnTextToSpeech                  func(handle int32, text string, options *cOption, optionsCount uint64, outAudioData *unsafe.Pointer, outAudioDataSize *uint64, outSampleRate *int32) int32
	fnGetTTSVoices                  func(languages string, options *cOption, optionsCount uint64, outVoicesJSON *unsafe.Pointer) int32

	fnGetSTTDependencies func(language string, options *cOption, optionsCount uint64, outJSON *unsafe.Pointer) int32
	fnGetTTSDependencies func(languages string, options *cOption, optionsCount uint64, outJSON *unsafe.Pointer) int32

	fnFree func(ptr unsafe.Pointer)
)

func registerSymbols(h uintptr) {
	reg := func(fptr any, name string) { purego.RegisterLibFunc(fptr, h, name) }

	reg(&fnGetVersion, "moonshine_get_version")
	reg(&fnErrorToString, "moonshine_error_to_string")

	reg(&fnLoadTranscriberFromFiles, "moonshine_load_transcriber_from_files")
	reg(&fnFreeTranscriber, "moonshine_free_transcriber")
	reg(&fnTranscribeWithoutStreaming, "moonshine_transcribe_without_streaming")
	reg(&fnCreateStream, "moonshine_create_stream")
	reg(&fnFreeStream, "moonshine_free_stream")
	reg(&fnStartStream, "moonshine_start_stream")
	reg(&fnStopStream, "moonshine_stop_stream")
	reg(&fnAddAudioToStream, "moonshine_transcribe_add_audio_to_stream")
	reg(&fnTranscribeStream, "moonshine_transcribe_stream")

	reg(&fnCreateTTSSynthesizerFromFiles, "moonshine_create_tts_synthesizer_from_files")
	reg(&fnFreeTTSSynthesizer, "moonshine_free_tts_synthesizer")
	reg(&fnTextToSpeech, "moonshine_text_to_speech")
	reg(&fnGetTTSVoices, "moonshine_get_tts_voices")

	reg(&fnGetSTTDependencies, "moonshine_get_stt_dependencies")
	reg(&fnGetTTSDependencies, "moonshine_get_tts_dependencies")

	// libc's free(), used to release buffers the moonshine API documents as
	// "allocated with malloc; release with free" (dependency/voice JSON,
	// synthesized audio). Resolved from the process's already-loaded
	// symbols rather than libmoonshine itself.
	purego.RegisterLibFunc(&fnFree, purego.RTLD_DEFAULT, "free")
}

func errorToString(code int32) string {
	if fnErrorToString == nil {
		return ""
	}
	return fnErrorToString(code)
}

// Version returns the loaded library's runtime version (moonshine_get_version),
// which may differ from HeaderVersion if a newer shared library is loaded.
func Version() (int32, error) {
	if !Loaded() {
		return 0, errNotLoaded
	}
	return fnGetVersion(), nil
}

// freeC releases a buffer the moonshine C API allocated with malloc.
func freeC(p unsafe.Pointer) {
	if p != nil && fnFree != nil {
		fnFree(p)
	}
}
