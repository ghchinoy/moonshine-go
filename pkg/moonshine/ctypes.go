package moonshine

import (
	"unsafe"
)

// The struct layouts below mirror moonshine-c-api.h exactly, including
// compiler padding, verified against `offsetof`/`sizeof` from the real
// header on darwin/arm64 and linux/amd64 (standard System V / AAPCS64
// alignment rules: every field aligns to its own size, pointers/uint64/
// float64 to 8 bytes, struct size rounds up to its largest member
// alignment). If moonshine-c-api.h changes these structs, this file must be
// updated to match.

// cOption mirrors `struct moonshine_option_t { const char *name; const char
// *value; }`.
type cOption struct {
	name  *byte
	value *byte
}

// cTranscriptWord mirrors `struct transcript_word_t`.
type cTranscriptWord struct {
	text       *byte
	start      float32
	end        float32
	confidence float32
	_          [4]byte // pad to 24 bytes (8-byte alignment)
}

// cSpeakerSpan mirrors `struct speaker_span_t`.
type cSpeakerSpan struct {
	startTime    float32
	duration     float32
	speakerID    uint64
	speakerIndex uint32
	_            [4]byte // pad before the next uint64 field
	startChar    uint64
	endChar      uint64
}

// cTranscriptLine mirrors `struct transcript_line_t`. Field order and
// explicit padding matter: this must byte-match the C layout.
type cTranscriptLine struct {
	text                       *byte
	audioData                  *float32
	audioDataCount             uint64
	startTime                  float32
	duration                   float32
	id                         uint64
	isComplete                 int8
	isUpdated                  int8
	isNew                      int8
	hasTextChanged             int8
	haveSpeakersChanged        int8
	_                          [3]byte // pad to 8-byte boundary before pointer field
	speakerSpans               *cSpeakerSpan
	speakerSpanCount           uint64
	lastTranscriptionLatencyMs uint32
	_                          [4]byte // pad to 8-byte boundary before pointer field
	words                      *cTranscriptWord
	wordCount                  uint64
}

// cTranscript mirrors `struct transcript_t`.
type cTranscript struct {
	lines     *cTranscriptLine
	lineCount uint64
}

// cString allocates a NUL-terminated byte buffer for s and returns a pointer
// to its first byte along with the backing slice (which the caller must keep
// alive, e.g. via runtime.KeepAlive, until the C call referencing the
// pointer has returned).
func cString(s string) (*byte, []byte) {
	b := make([]byte, len(s)+1)
	copy(b, s)
	if len(b) == 0 {
		return nil, b
	}
	return &b[0], b
}

// goString converts a NUL-terminated C string to a Go string. Does not free
// the underlying C memory -- see freeC for buffers that must be released.
func goString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for {
		b := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(n)))
		if b == 0 {
			break
		}
		n++
	}
	return string(unsafe.Slice(p, n))
}

// goFloat32Slice copies a C float* array of length n into a new Go slice.
func goFloat32Slice(p *float32, n uint64) []float32 {
	if p == nil || n == 0 {
		return nil
	}
	src := unsafe.Slice(p, int(n))
	out := make([]float32, n)
	copy(out, src)
	return out
}

// toCOptions converts a map of option name/value pairs into a C array of
// moonshine_option_t. Returns the array pointer (or nil if empty), the
// element count, and the set of backing byte slices that must be kept alive
// (via runtime.KeepAlive) until the call using the array has returned.
func toCOptions(opts []Option) (*cOption, uint64, []any) {
	if len(opts) == 0 {
		return nil, 0, nil
	}
	keepAlive := make([]any, 0, len(opts)*2+1)
	arr := make([]cOption, len(opts))
	for i, o := range opts {
		namePtr, nameBuf := cString(o.Name)
		valPtr, valBuf := cString(o.Value)
		arr[i] = cOption{name: namePtr, value: valPtr}
		keepAlive = append(keepAlive, nameBuf, valBuf)
	}
	keepAlive = append(keepAlive, arr)
	return &arr[0], uint64(len(arr)), keepAlive
}
