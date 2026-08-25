package moonshine

import (
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"time"
	"unsafe"
)

// TTSSampleRateHz is MoonshineTTS::kSampleRateHz -- the fixed output sample
// rate for all moonshine TTS voices (Kokoro, Piper, ZipVoice).
const TTSSampleRateHz = 24000

// Audio is a synthesized waveform: mono float32 PCM in [-1, 1].
type Audio struct {
	Samples    []float32
	SampleRate int32
}

// Duration returns the audio's playback length.
func (a Audio) Duration() time.Duration {
	if a.SampleRate == 0 {
		return 0
	}
	secs := float64(len(a.Samples)) / float64(a.SampleRate)
	return time.Duration(secs * float64(time.Second))
}

// Synthesizer wraps a moonshine TTS synthesizer handle
// (moonshine_create_tts_synthesizer_from_files /
// moonshine_create_tts_synthesizer_from_memory).
type Synthesizer struct {
	handle     int32
	language   string
	closed     bool
	pcmBuffers [][]byte // backing buffer references kept alive while handle is open
}

// TTSChunk is one piece of synthesized audio from a streaming TTS session.
// It embeds Audio so Samples, SampleRate, and Duration() are directly accessible.
type TTSChunk struct {
	Audio
	Text        string
	UtteranceID uint64
	IsFinal     bool
}

// NewSynthesizer creates a TTS synthesizer for language (a moonshine
// language/CLI tag such as "en_us"). All model/voice selection is driven by
// opts -- see moonshine-c-api.h for recognized keys, notably:
//
//	"voice"       kokoro_<id> / piper_<stem> / zipvoice_<id> (default: auto)
//	"g2p_root"    directory holding kokoro/, piper-voices/, etc. (aliases:
//	              "model_root", "path_root", "tts_root")
//	"speed"       synthesis speed multiplier (default 1.0)
func NewSynthesizer(language string, opts ...Option) (*Synthesizer, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	cOpts, optCount, keep := toCOptions(opts)
	h := fnCreateTTSSynthesizerFromFiles(language, nil, 0, cOpts, optCount, HeaderVersion)
	runtime.KeepAlive(keep)
	handle, err := checkHandle("create_tts_synthesizer_from_files", h)
	if err != nil {
		return nil, err
	}
	s := &Synthesizer{handle: handle, language: language}
	runtime.SetFinalizer(s, func(sy *Synthesizer) { _ = sy.Close() })
	return s, nil
}

// NewSynthesizerFromClone creates a TTS synthesizer configured for zero-shot
// voice cloning via ZipVoice using a reference audio clip (pcm mono float32
// samples in [-1, 1]).
//
// pcm is the reference audio waveform. sampleRate is the sample rate of pcm
// (e.g. 16000 or 24000). transcript is the text spoken in the reference clip
// (strongly recommended for optimal voice cloning accuracy).
//
// Additional options (e.g. "g2p_root", "speed") may be passed in opts.
func NewSynthesizerFromClone(language string, pcm []float32, sampleRate int32, transcript string, opts ...Option) (*Synthesizer, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("moonshine: clone pcm audio cannot be empty")
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("moonshine: invalid clone sample rate %d", sampleRate)
	}

	// Convert float32 slice to little-endian byte buffer
	pcmBytes := make([]byte, len(pcm)*4)
	for i, sample := range pcm {
		bits := math.Float32bits(sample)
		pcmBytes[i*4] = byte(bits)
		pcmBytes[i*4+1] = byte(bits >> 8)
		pcmBytes[i*4+2] = byte(bits >> 16)
		pcmBytes[i*4+3] = byte(bits >> 24)
	}

	keyPtr, keyBuf := cString("zipvoice/clone_audio")
	keyArray := []*byte{keyPtr}
	memArray := []*byte{&pcmBytes[0]}
	memSizeArray := []uint64{uint64(len(pcmBytes))}

	cloneOpts := append([]Option{
		{Name: "voice", Value: "zipvoice"},
		{Name: "zipvoice_clone_sample_rate", Value: fmt.Sprintf("%d", sampleRate)},
	}, opts...)
	if transcript != "" {
		cloneOpts = append(cloneOpts, Option{Name: "zipvoice_clone_transcript", Value: transcript})
	}

	cOpts, optCount, keep := toCOptions(cloneOpts)

	h := fnCreateTTSSynthesizerFromMemory(
		language,
		unsafe.Pointer(&keyArray[0]), 1,
		unsafe.Pointer(&memArray[0]),
		unsafe.Pointer(&memSizeArray[0]),
		cOpts, optCount,
		HeaderVersion,
	)
	runtime.KeepAlive(keep)
	runtime.KeepAlive(keyBuf)
	runtime.KeepAlive(keyArray)
	runtime.KeepAlive(memArray)
	runtime.KeepAlive(memSizeArray)
	runtime.KeepAlive(pcmBytes)

	handle, err := checkHandle("create_tts_synthesizer_from_memory", h)
	if err != nil {
		return nil, err
	}

	s := &Synthesizer{
		handle:     handle,
		language:   language,
		pcmBuffers: [][]byte{pcmBytes},
	}
	runtime.SetFinalizer(s, func(sy *Synthesizer) { _ = sy.Close() })
	return s, nil
}

// Close releases the synthesizer's resources. Safe to call more than once.
func (s *Synthesizer) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	runtime.SetFinalizer(s, nil)
	fnFreeTTSSynthesizer(s.handle)
	s.pcmBuffers = nil
	return nil
}

// Synthesize converts text to speech. Per-call option overrides (currently
// only "speed" is honored) may be passed in opts; pass none to use the
// synthesizer's default from construction.
func (s *Synthesizer) Synthesize(text string, opts ...Option) (Audio, error) {
	if !Loaded() {
		return Audio{}, errNotLoaded
	}
	if s.closed {
		return Audio{}, errClosed
	}
	cOpts, optCount, keep := toCOptions(opts)
	var audioPtr unsafe.Pointer
	var audioSize uint64
	var sampleRate int32
	code := fnTextToSpeech(s.handle, text, cOpts, optCount, &audioPtr, &audioSize, &sampleRate)
	runtime.KeepAlive(keep)
	if err := checkCode("text_to_speech", code); err != nil {
		return Audio{}, err
	}
	defer freeC(audioPtr)
	samples := goFloat32Slice((*float32)(audioPtr), audioSize)
	return Audio{Samples: samples, SampleRate: sampleRate}, nil
}

// PhonemesToSpeech synthesizes speech directly from an International
// Phonetic Alphabet (IPA) phonemes string, skipping the grapheme-to-phoneme
// conversion Synthesize performs internally. phonemes should be in the
// format produced by (*Phonemizer).TextToPhonemes for a matching language --
// this is the "edit" half of an inspect-and-edit workflow, e.g. hand-fixing
// how a proper noun gets pronounced between TextToPhonemes and
// PhonemesToSpeech. Only "speed" is honored in opts, same as Synthesize.
func (s *Synthesizer) PhonemesToSpeech(phonemes string, opts ...Option) (Audio, error) {
	if !Loaded() {
		return Audio{}, errNotLoaded
	}
	if s.closed {
		return Audio{}, errClosed
	}
	cOpts, optCount, keep := toCOptions(opts)
	var audioPtr unsafe.Pointer
	var audioSize uint64
	var sampleRate int32
	code := fnPhonemesToSpeech(s.handle, phonemes, cOpts, optCount, &audioPtr, &audioSize, &sampleRate)
	runtime.KeepAlive(keep)
	if err := checkCode("phonemes_to_speech", code); err != nil {
		return Audio{}, err
	}
	defer freeC(audioPtr)
	samples := goFloat32Slice((*float32)(audioPtr), audioSize)
	return Audio{Samples: samples, SampleRate: sampleRate}, nil
}

// VoiceAvailability is one known TTS voice id and whether its assets are
// available (on disk, under the resolved g2p_root/model_root).
type VoiceAvailability struct {
	ID    string
	Found bool
}

// ListVoices returns known TTS voices per language (comma-separated tags in
// languages, or "" for all registered languages) via
// moonshine_get_tts_voices. Set "g2p_root"/"model_root" in opts for accurate
// Found state.
func ListVoices(languages string, opts ...Option) (map[string][]VoiceAvailability, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	cOpts, optCount, keep := toCOptions(opts)
	var outPtr unsafe.Pointer
	code := fnGetTTSVoices(languages, cOpts, optCount, &outPtr)
	runtime.KeepAlive(keep)
	if err := checkCode("get_tts_voices", code); err != nil {
		return nil, err
	}
	defer freeC(outPtr)
	raw := goString((*byte)(outPtr))

	var parsed map[string][]struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("moonshine: parsing tts voices JSON: %w", err)
	}
	out := make(map[string][]VoiceAvailability, len(parsed))
	for lang, voices := range parsed {
		vs := make([]VoiceAvailability, len(voices))
		for i, v := range voices {
			vs[i] = VoiceAvailability{ID: v.ID, Found: v.State == "found"}
		}
		out[lang] = vs
	}
	return out, nil
}

// PushText appends text to the streaming TTS buffer. Pieces are concatenated
// verbatim, making it suitable for streaming token-by-token output from an LLM.
// If no streaming generation is currently running, pushing text starts one.
//
// Text is buffered until a complete utterance boundary is detected according to
// language rules, ensuring natural prosody. Use Flush or EndInput to force
// synthesis of incomplete trailing text.
//
// Returns ErrBusy if another thread or caller is concurrently streaming, or
// an Error on failure.
func (s *Synthesizer) PushText(text string) error {
	if !Loaded() {
		return errNotLoaded
	}
	if s.closed {
		return errClosed
	}
	code := fnTTSPushText(s.handle, text)
	if code == ErrorCodeBusy {
		return ErrBusy
	}
	return checkCode("tts_push_text", code)
}

// Flush forces synthesis of whatever text is currently buffered even if it does
// not yet form a complete sentence.
func (s *Synthesizer) Flush() error {
	if !Loaded() {
		return errNotLoaded
	}
	if s.closed {
		return errClosed
	}
	code := fnTTSFlush(s.handle)
	if code == ErrorCodeBusy {
		return ErrBusy
	}
	return checkCode("tts_flush", code)
}

// EndInput declares that no more text will be pushed for the current stream.
// It flushes buffered text and signals that NextChunk will return ErrEndOfStream
// once all remaining audio chunks have been synthesized and drained.
func (s *Synthesizer) EndInput() error {
	if !Loaded() {
		return errNotLoaded
	}
	if s.closed {
		return errClosed
	}
	code := fnTTSEndInput(s.handle)
	if code == ErrorCodeBusy {
		return ErrBusy
	}
	return checkCode("tts_end_input", code)
}

// Cancel drops queued text, abandons the active streaming generation in progress,
// and returns the synthesizer to idle. This is the barge-in path when a user
// interrupts voice playback. Safe to call even when no stream is active.
func (s *Synthesizer) Cancel() error {
	if !Loaded() {
		return errNotLoaded
	}
	if s.closed {
		return errClosed
	}
	code := fnTTSCancel(s.handle)
	return checkCode("tts_cancel", code)
}

// IsStreaming reports whether a streaming generation is currently in flight.
func (s *Synthesizer) IsStreaming() bool {
	if !Loaded() || s.closed || fnTTSIsStreaming == nil {
		return false
	}
	return fnTTSIsStreaming(s.handle) != 0
}

// NextChunk synthesizes and returns the next chunk of audio from the stream.
// It is synchronous and pull-based:
//   - Returns (*TTSChunk, nil) when an audio chunk is produced.
//   - Returns (nil, ErrNeedText) when more text is needed to form a sentence (push more or flush).
//   - Returns (nil, ErrEndOfStream) after EndInput has been called and all audio drained.
//   - Returns (nil, ErrCancelled) once after Cancel has discarded an active generation.
//   - Returns (nil, error) on C API errors.
func (s *Synthesizer) NextChunk() (*TTSChunk, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	if s.closed {
		return nil, errClosed
	}
	var chunkPtr unsafe.Pointer
	code := fnTTSNextChunk(s.handle, 0, &chunkPtr)
	switch code {
	case ErrorCodeNone:
		if chunkPtr == nil {
			return nil, nil
		}
		cChunk := (*cTTSChunk)(chunkPtr)
		samples := goFloat32Slice(cChunk.audioData, cChunk.audioDataCount)
		text := goString(cChunk.text)
		return &TTSChunk{
			Audio: Audio{
				Samples:    samples,
				SampleRate: cChunk.sampleRate,
			},
			Text:        text,
			UtteranceID: cChunk.utteranceID,
			IsFinal:     cChunk.isFinal != 0,
		}, nil
	case TTSStatusNeedText:
		return nil, ErrNeedText
	case TTSStatusEndOfStream:
		return nil, ErrEndOfStream
	case TTSStatusCancelled:
		return nil, ErrCancelled
	case ErrorCodeBusy:
		return nil, ErrBusy
	default:
		return nil, checkCode("tts_next_chunk", code)
	}
}

// TTSStream represents an active streaming TTS generation on a Synthesizer.
// It exposes the streaming lifecycle methods on a dedicated stream handle.
type TTSStream struct {
	synth *Synthesizer
}

// NewStream creates a new streaming handle bound to this Synthesizer.
func (s *Synthesizer) NewStream() *TTSStream {
	return &TTSStream{synth: s}
}

// PushText appends text to the stream.
func (st *TTSStream) PushText(text string) error {
	return st.synth.PushText(text)
}

// Flush forces synthesis of buffered text.
func (st *TTSStream) Flush() error {
	return st.synth.Flush()
}

// EndInput marks end of text input and flushes remaining audio.
func (st *TTSStream) EndInput() error {
	return st.synth.EndInput()
}

// Cancel abandons current generation for barge-in.
func (st *TTSStream) Cancel() error {
	return st.synth.Cancel()
}

// IsStreaming reports whether generation is active.
func (st *TTSStream) IsStreaming() bool {
	return st.synth.IsStreaming()
}

// NextChunk retrieves the next synthesized audio chunk.
func (st *TTSStream) NextChunk() (*TTSChunk, error) {
	return st.synth.NextChunk()
}

// SplitUtterances splits text into sentence/utterance units using Moonshine's
// language-aware rules (abbreviations, terminators).
//
// Recognised options:
//
//	"split_on_colon"  (bool, default true) break after ":" so lead-ins start early
//	"min_codepoints"  (int, default 0) merge units shorter than this into the next unit
func SplitUtterances(language string, text string, opts ...Option) ([]string, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	cOpts, optCount, keep := toCOptions(opts)
	var outPtr unsafe.Pointer
	code := fnTTSSplitUtterances(language, text, cOpts, optCount, &outPtr)
	runtime.KeepAlive(keep)
	if err := checkCode("tts_split_utterances", code); err != nil {
		return nil, err
	}
	defer freeC(outPtr)
	raw := goString((*byte)(outPtr))
	var units []string
	if err := json.Unmarshal([]byte(raw), &units); err != nil {
		return nil, fmt.Errorf("moonshine: parsing split utterances JSON: %w", err)
	}
	return units, nil
}

// SplitUtterances splits text into sentence/utterance units using this synthesizer's language rules.
func (s *Synthesizer) SplitUtterances(text string, opts ...Option) ([]string, error) {
	return SplitUtterances(s.language, text, opts...)
}
