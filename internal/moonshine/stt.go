package moonshine

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"unsafe"
)

// Model architectures, mirroring MOONSHINE_MODEL_ARCH_* in moonshine-c-api.h.
const (
	ModelArchTiny            uint32 = 0
	ModelArchBase            uint32 = 1
	ModelArchTinyStreaming   uint32 = 2
	ModelArchBaseStreaming   uint32 = 3
	ModelArchSmallStreaming  uint32 = 4
	ModelArchMediumStreaming uint32 = 5
)

// Flags, mirroring MOONSHINE_FLAG_* in moonshine-c-api.h.
const (
	FlagForceUpdate  uint32 = 1 << 0
	FlagSpellingMode uint32 = 1 << 1
)

// SampleRate is the sample rate (Hz) audio should be captured/decoded at to
// avoid internal resampling.
const SampleRate = 16000

var errClosed = errors.New("moonshine: handle is closed")

// Word is a single word with timing information. Only populated when the
// "word_timestamps" option is enabled.
type Word struct {
	Text       string  `json:"text"`
	Start      float32 `json:"start"`
	End        float32 `json:"end"`
	Confidence float32 `json:"confidence"`
}

// SpeakerSpan is one contiguous span of speech attributed to a single
// speaker. Only populated when the "identify_speakers" option is enabled.
type SpeakerSpan struct {
	StartTime    float32 `json:"start_time"`
	Duration     float32 `json:"duration"`
	SpeakerID    uint64  `json:"speaker_id"`
	SpeakerIndex uint32  `json:"speaker_index"`
	StartChar    uint64  `json:"start_char"`
	EndChar      uint64  `json:"end_char"`
}

// Line is one "line" (roughly a sentence or phrase) of a transcript. For
// streaming results, IsComplete distinguishes finalized lines from the
// (at most one) trailing in-progress line.
type Line struct {
	Text                string        `json:"text"`
	AudioData           []float32     `json:"audio_data,omitempty"`
	StartTime           float32       `json:"start_time"`
	Duration            float32       `json:"duration"`
	ID                  uint64        `json:"id"`
	IsComplete          bool          `json:"is_complete"`
	IsUpdated           bool          `json:"is_updated"`
	IsNew               bool          `json:"is_new"`
	HasTextChanged      bool          `json:"has_text_changed"`
	HaveSpeakersChanged bool          `json:"have_speakers_changed"`
	LastLatencyMs       uint32        `json:"last_latency_ms"`
	Words               []Word        `json:"words,omitempty"`
	SpeakerSpans        []SpeakerSpan `json:"speaker_spans,omitempty"`
}

// Transcript is a full (non-streaming) or partial (streaming) transcription
// result: an ordered list of Lines.
type Transcript struct {
	Lines []Line
}

// SpeakerLabel returns a compact summary of which speaker(s) contributed to
// this line, e.g. "S0" or "S0+S1", derived from SpeakerSpans (only populated
// when the "identify_speakers" option is enabled). Returns "" if there are
// no speaker spans -- either identify_speakers wasn't enabled, or no speech
// has been attributed to a speaker yet.
func (l Line) SpeakerLabel() string {
	if len(l.SpeakerSpans) == 0 {
		return ""
	}
	seen := map[uint32]bool{}
	indices := make([]uint32, 0, len(l.SpeakerSpans))
	for _, s := range l.SpeakerSpans {
		if !seen[s.SpeakerIndex] {
			seen[s.SpeakerIndex] = true
			indices = append(indices, s.SpeakerIndex)
		}
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })
	parts := make([]string, len(indices))
	for i, idx := range indices {
		parts[i] = fmt.Sprintf("S%d", idx)
	}
	return strings.Join(parts, "+")
}

// WordTimingsSummary renders each word with its start time as a compact
// "word@1.23" list, space-separated -- only meaningful when the
// "word_timestamps" option was enabled (populating l.Words). Returns "" if
// there are no words.
func (l Line) WordTimingsSummary() string {
	if len(l.Words) == 0 {
		return ""
	}
	parts := make([]string, len(l.Words))
	for i, w := range l.Words {
		parts[i] = fmt.Sprintf("%s@%.2f", w.Text, w.Start)
	}
	return strings.Join(parts, " ")
}

// FirstNonEmptyText returns the text of the first line with non-empty text,
// and true, or ("", false) if there isn't one yet. Useful for detecting
// time-to-first-token in a streaming session.
func (t Transcript) FirstNonEmptyText() (string, bool) {
	for _, l := range t.Lines {
		if l.Text != "" {
			return l.Text, true
		}
	}
	return "", false
}

func copyTranscript(p unsafe.Pointer) Transcript {
	if p == nil {
		return Transcript{}
	}
	t := (*cTranscript)(p)
	if t.lines == nil || t.lineCount == 0 {
		return Transcript{}
	}
	cLines := unsafe.Slice(t.lines, int(t.lineCount))
	lines := make([]Line, len(cLines))
	for i, cl := range cLines {
		line := Line{
			Text:                goString(cl.text),
			AudioData:           goFloat32Slice(cl.audioData, cl.audioDataCount),
			StartTime:           cl.startTime,
			Duration:            cl.duration,
			ID:                  cl.id,
			IsComplete:          cl.isComplete != 0,
			IsUpdated:           cl.isUpdated != 0,
			IsNew:               cl.isNew != 0,
			HasTextChanged:      cl.hasTextChanged != 0,
			HaveSpeakersChanged: cl.haveSpeakersChanged != 0,
			LastLatencyMs:       cl.lastTranscriptionLatencyMs,
		}
		if cl.words != nil && cl.wordCount > 0 {
			cWords := unsafe.Slice(cl.words, int(cl.wordCount))
			line.Words = make([]Word, len(cWords))
			for j, w := range cWords {
				line.Words[j] = Word{Text: goString(w.text), Start: w.start, End: w.end, Confidence: w.confidence}
			}
		}
		if cl.speakerSpans != nil && cl.speakerSpanCount > 0 {
			cSpans := unsafe.Slice(cl.speakerSpans, int(cl.speakerSpanCount))
			line.SpeakerSpans = make([]SpeakerSpan, len(cSpans))
			for j, s := range cSpans {
				line.SpeakerSpans[j] = SpeakerSpan{
					StartTime: s.startTime, Duration: s.duration,
					SpeakerID: s.speakerID, SpeakerIndex: s.speakerIndex,
					StartChar: s.startChar, EndChar: s.endChar,
				}
			}
		}
		lines[i] = line
	}
	return Transcript{Lines: lines}
}

// Transcriber wraps a moonshine transcriber handle (moonshine_load_transcriber_from_files).
type Transcriber struct {
	handle int32
	closed bool
}

// LoadTranscriber loads STT models from modelDir (expects encoder_model.ort,
// decoder_model_merged.ort, tokenizer.bin -- see moonshine setup / T1.5's
// download manifest helpers). arch selects the model architecture (one of
// the ModelArch* constants). See moonshine-c-api.h for recognized options
// (e.g. "ort_providers", "identify_speakers", "spelling_model_path").
func LoadTranscriber(modelDir string, arch uint32, opts ...Option) (*Transcriber, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	cOpts, optCount, keep := toCOptions(opts)
	h := fnLoadTranscriberFromFiles(modelDir, arch, cOpts, optCount, HeaderVersion)
	runtime.KeepAlive(keep)
	handle, err := checkHandle("load_transcriber_from_files", h)
	if err != nil {
		return nil, err
	}
	tr := &Transcriber{handle: handle}
	runtime.SetFinalizer(tr, func(t *Transcriber) { _ = t.Close() })
	return tr, nil
}

// Close releases the transcriber's resources. Safe to call more than once.
func (t *Transcriber) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true
	runtime.SetFinalizer(t, nil)
	fnFreeTranscriber(t.handle)
	return nil
}

// Transcribe runs non-streaming transcription over a full array of PCM audio
// (float32, range [-1, 1]) at the given sample rate. Prefer this for
// analyzing complete files; use NewStream for live/incremental audio.
func (t *Transcriber) Transcribe(audio []float32, sampleRate int32, flags uint32) (Transcript, error) {
	if !Loaded() {
		return Transcript{}, errNotLoaded
	}
	if t.closed {
		return Transcript{}, errClosed
	}
	var audioPtr *float32
	if len(audio) > 0 {
		audioPtr = &audio[0]
	}
	var out unsafe.Pointer
	code := fnTranscribeWithoutStreaming(t.handle, audioPtr, uint64(len(audio)), sampleRate, flags, &out)
	runtime.KeepAlive(audio)
	if err := checkCode("transcribe_without_streaming", code); err != nil {
		return Transcript{}, err
	}
	// out is owned by the transcriber (valid until the next call or Close);
	// we copy it into Go memory immediately and never free it ourselves.
	return copyTranscript(out), nil
}

// Stream wraps a moonshine streaming handle for low-latency, incremental
// transcription (e.g. from a live microphone).
type Stream struct {
	transcriber *Transcriber
	handle      int32
	closed      bool
}

// NewStream creates a stream bound to this transcriber. A single transcriber
// can back multiple concurrent streams.
func (t *Transcriber) NewStream(flags uint32) (*Stream, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	if t.closed {
		return nil, errClosed
	}
	h := fnCreateStream(t.handle, flags)
	handle, err := checkHandle("create_stream", h)
	if err != nil {
		return nil, err
	}
	s := &Stream{transcriber: t, handle: handle}
	runtime.SetFinalizer(s, func(st *Stream) { _ = st.Close() })
	return s, nil
}

// Start must be called before AddAudio/Transcribe, and again after Stop if
// there's a discontinuity in the input audio (e.g. the mic was muted).
func (s *Stream) Start() error {
	if s.closed {
		return errClosed
	}
	return checkCode("start_stream", fnStartStream(s.transcriber.handle, s.handle))
}

// Stop should be called before reading the final transcript at the end of a
// session.
func (s *Stream) Stop() error {
	if s.closed {
		return errClosed
	}
	return checkCode("stop_stream", fnStopStream(s.transcriber.handle, s.handle))
}

// AddAudio appends newly-captured PCM audio to the stream's buffer. Cheap
// and safe to call frequently (e.g. from an audio callback); it does not
// itself trigger transcription -- call Transcribe when you want updated
// results.
func (s *Stream) AddAudio(audio []float32, sampleRate int32) error {
	if s.closed {
		return errClosed
	}
	var audioPtr *float32
	if len(audio) > 0 {
		audioPtr = &audio[0]
	}
	code := fnAddAudioToStream(s.transcriber.handle, s.handle, audioPtr, uint64(len(audio)), sampleRate, 0)
	runtime.KeepAlive(audio)
	return checkCode("transcribe_add_audio_to_stream", code)
}

// Transcribe analyzes all buffered audio and returns an updated transcript.
// By default the library skips re-analysis if there's been less than ~200ms
// of new audio since the last call; pass FlagForceUpdate to override that.
func (s *Stream) Transcribe(flags uint32) (Transcript, error) {
	if s.closed {
		return Transcript{}, errClosed
	}
	var out unsafe.Pointer
	code := fnTranscribeStream(s.transcriber.handle, s.handle, flags, &out)
	if err := checkCode("transcribe_stream", code); err != nil {
		return Transcript{}, err
	}
	return copyTranscript(out), nil
}

// Close releases the stream's resources. Safe to call more than once.
func (s *Stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	runtime.SetFinalizer(s, nil)
	return checkCode("free_stream", fnFreeStream(s.transcriber.handle, s.handle))
}
