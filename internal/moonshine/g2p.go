package moonshine

import (
	"runtime"
	"unsafe"
)

// Phonemizer wraps a moonshine grapheme-to-phonemizer handle
// (moonshine_create_grapheme_to_phonemizer_from_files) -- the G2P
// (text-normalization/phonemization) step moonshine_text_to_speech runs
// internally. Binding it directly lets a caller inspect (and hand-edit) the
// International Phonetic Alphabet (IPA) string before handing it to
// (*Synthesizer).PhonemesToSpeech, e.g. to fix a mispronounced proper noun.
type Phonemizer struct {
	handle int32
	closed bool
}

// NewPhonemizer creates a grapheme-to-phonemizer for language (a moonshine
// language/CLI tag such as "en_us"). Recognized opts keys (see
// moonshine-c-api.h):
//
//	"g2p_root"    directory holding the G2P lexicons/ONNX assets (aliases:
//	              "model_root", "path_root", "tts_root" -- same aliasing
//	              NewSynthesizer's g2p_root uses, since it's the same
//	              underlying G2P layer)
//	"language"    overrides the language argument (alias: "lang")
func NewPhonemizer(language string, opts ...Option) (*Phonemizer, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	cOpts, optCount, keep := toCOptions(opts)
	h := fnCreateGraphemeToPhonemizerFromFiles(language, nil, 0, cOpts, optCount, HeaderVersion)
	runtime.KeepAlive(keep)
	handle, err := checkHandle("create_grapheme_to_phonemizer_from_files", h)
	if err != nil {
		return nil, err
	}
	p := &Phonemizer{handle: handle}
	runtime.SetFinalizer(p, func(ph *Phonemizer) { _ = ph.Close() })
	return p, nil
}

// Close releases the phonemizer's resources. Safe to call more than once.
func (p *Phonemizer) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	runtime.SetFinalizer(p, nil)
	fnFreeGraphemeToPhonemizer(p.handle)
	return nil
}

// TextToPhonemes converts text to an International Phonetic Alphabet (IPA)
// string in the phonemizer's language, using the same G2P pipeline
// moonshine_text_to_speech runs internally. The result is in the format
// (*Synthesizer).PhonemesToSpeech expects.
func (p *Phonemizer) TextToPhonemes(text string, opts ...Option) (string, error) {
	if !Loaded() {
		return "", errNotLoaded
	}
	if p.closed {
		return "", errClosed
	}
	cOpts, optCount, keep := toCOptions(opts)
	var outPtr unsafe.Pointer
	var outCount uint64
	code := fnTextToPhonemes(p.handle, text, cOpts, optCount, &outPtr, &outCount)
	runtime.KeepAlive(keep)
	if err := checkCode("text_to_phonemes", code); err != nil {
		return "", err
	}
	defer freeC(outPtr)
	return goString((*byte)(outPtr)), nil
}
