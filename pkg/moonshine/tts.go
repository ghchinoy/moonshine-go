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
	closed     bool
	pcmBuffers [][]byte // backing buffer references kept alive while handle is open
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
	s := &Synthesizer{handle: handle}
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
