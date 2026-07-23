package serve

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/moonshine"
)

// TTSSpeaker implements Speaker using a lazily-constructed
// moonshine.Synthesizer and audio.PlayFloat32 for playback. It is the
// "speak-back" half of the sidecar's voice loop.
//
// Barge-in guard: TTSSpeaker exposes Speaking(), which reports true for
// the duration of a Speak call. The mic-feed loop (owned by
// cmd/moonshine/serve.go, P6) must check this before forwarding each mic
// chunk to the STT stream, so the sidecar's own synthesized voice is never
// fed back into transcription. This is a simple mute -- it stops feeding
// audio into the stream while speaking -- not acoustic echo cancellation:
// if something else in the room is still making noise during playback
// (e.g. a second person talking over the TTS), that audio is also
// suppressed for the duration, and any echo picked up by the mic simply
// never reaches the transcriber rather than being cancelled out
// acoustically. Document this limitation wherever Speaking() is consumed.
type TTSSpeaker struct {
	language string
	baseOpts []moonshine.Option

	mu     sync.Mutex
	synth  *moonshine.Synthesizer // lazily created on first Speak
	closed bool

	speaking atomic.Bool
}

// NewTTSSpeaker creates a Speaker for language (e.g. "en_us"), passing
// baseOpts to moonshine.NewSynthesizer on first use (e.g. "g2p_root",
// "voice", "model_root" -- see moonshine.NewSynthesizer's doc comment).
// The synthesizer itself is not constructed until the first Speak call, so
// constructing a TTSSpeaker never touches libmoonshine (keeping
// e.g. `moonshine serve --allow-actions=false` free of TTS model loading).
func NewTTSSpeaker(language string, baseOpts ...moonshine.Option) *TTSSpeaker {
	return &TTSSpeaker{language: language, baseOpts: baseOpts}
}

// Speak synthesizes text and plays it through the default output device,
// blocking until playback finishes. voice/speed, when non-empty/non-zero,
// are passed as per-call "voice"/"speed" option overrides (see
// moonshine.(*Synthesizer).Synthesize); otherwise the synthesizer's
// construction-time defaults apply.
//
// ctx is checked before synthesis and before playback begins, but is NOT
// able to interrupt an in-progress audio.PlayFloat32 call (which sleeps
// for the clip's fixed duration and has no cancellation hook of its own,
// internal/audio/playback.go:71) -- a cancelled ctx during playback lets
// the current utterance finish rather than cutting it off abruptly. This
// is a deliberate v1 limitation; interruptible playback would require
// changes to audio.PlayFloat32 itself, tracked as follow-up if barge-in
// (a human interrupting the agent mid-sentence) becomes a requirement
// beyond "don't transcribe our own voice".
func (s *TTSSpeaker) Speak(ctx context.Context, text, voice string, speed float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	synth, err := s.synthesizer()
	if err != nil {
		return err
	}

	var opts []moonshine.Option
	if voice != "" {
		opts = append(opts, moonshine.Option{Name: "voice", Value: voice})
	}
	if speed > 0 {
		opts = append(opts, moonshine.Option{Name: "speed", Value: fmt.Sprintf("%g", speed)})
	}

	audioOut, err := synth.Synthesize(text, opts...)
	if err != nil {
		return fmt.Errorf("serve: tts synthesize: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.speaking.Store(true)
	defer s.speaking.Store(false)

	if err := audio.PlayFloat32(audioOut.Samples, audioOut.SampleRate); err != nil {
		return fmt.Errorf("serve: tts playback: %w", err)
	}
	return nil
}

// Speaking reports whether a Speak call is currently synthesizing or
// playing audio. See TTSSpeaker's doc comment for the barge-in guard this
// exists to support.
func (s *TTSSpeaker) Speaking() bool { return s.speaking.Load() }

// Close releases the underlying synthesizer, if one was created. Safe to
// call even if Speak was never called.
func (s *TTSSpeaker) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.synth != nil {
		return s.synth.Close()
	}
	return nil
}

func (s *TTSSpeaker) synthesizer() (*moonshine.Synthesizer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("serve: tts speaker is closed")
	}
	if s.synth != nil {
		return s.synth, nil
	}
	synth, err := moonshine.NewSynthesizer(s.language, s.baseOpts...)
	if err != nil {
		return nil, fmt.Errorf("serve: creating tts synthesizer: %w", err)
	}
	s.synth = synth
	return synth, nil
}

var _ Speaker = (*TTSSpeaker)(nil)
