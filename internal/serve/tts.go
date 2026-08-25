package serve

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
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

	mu        sync.Mutex
	synths    map[string]*moonshine.Synthesizer // keyed by "voice:speed"
	closed    bool
	publisher Publisher
	playLocal bool

	speaking atomic.Bool
}

// NewTTSSpeaker creates a Speaker for language (e.g. "en_us"), passing
// baseOpts to moonshine.NewSynthesizer on first use (e.g. "g2p_root",
// "voice", "model_root" -- see moonshine.NewSynthesizer's doc comment).
// The synthesizer itself is not constructed until the first Speak call, so
// constructing a TTSSpeaker never touches libmoonshine (keeping
// e.g. `moonshine serve --allow-actions=false` free of TTS model loading).
func NewTTSSpeaker(language string, baseOpts ...moonshine.Option) *TTSSpeaker {
	return &TTSSpeaker{
		language:  language,
		baseOpts:  baseOpts,
		synths:    make(map[string]*moonshine.Synthesizer),
		playLocal: true,
	}
}

// SetPublisher configures a Publisher (e.g. Hub) for emitting TTSAudioEvent wire
// events over transports. If playLocal is false, local audio.PlayFloat32 playback
// is skipped (for hosted/remote use).
func (s *TTSSpeaker) SetPublisher(publisher Publisher, playLocal bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publisher = publisher
	s.playLocal = playLocal
}

// Speak synthesizes text and plays it through the default output device,
// using streaming TTS to synthesize and play chunks incrementally with low latency.
// voice/speed, when non-empty/non-zero, are passed as per-call option overrides.
//
// When pub is non-nil (or configured on TTSSpeaker), emits "start", individual "chunk",
// and "end" (or "interrupted") TTSAudioEvent envelopes.
func (s *TTSSpeaker) Speak(ctx context.Context, pub Publisher, text, voice string, speed float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	synth, err := s.synthesizer(voice, speed)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if pub == nil {
		pub = s.publisher
	}
	playLocal := s.playLocal
	s.mu.Unlock()

	s.speaking.Store(true)
	defer s.speaking.Store(false)

	if pub != nil {
		pub.Publish(event.TTSAudioEvent{
			Text:  text,
			State: "start",
		})
	}

	stream := synth.NewStream()

	if err := stream.PushText(text); err != nil {
		return fmt.Errorf("serve: tts push text: %w", err)
	}
	if err := stream.EndInput(); err != nil {
		return fmt.Errorf("serve: tts end input: %w", err)
	}

	var lastSampleRate int
	interrupted := false

	for {
		if ctx.Err() != nil || !s.speaking.Load() {
			interrupted = true
			_ = stream.Cancel()
			break
		}

		chunk, err := stream.NextChunk()
		if err == moonshine.ErrEndOfStream {
			break
		}
		if err == moonshine.ErrCancelled {
			interrupted = true
			break
		}
		if err == moonshine.ErrNeedText {
			break
		}
		if err != nil {
			return fmt.Errorf("serve: tts next chunk: %w", err)
		}

		if chunk != nil && len(chunk.Samples) > 0 {
			lastSampleRate = int(chunk.SampleRate)
			if pub != nil {
				pub.Publish(event.TTSAudioEvent{
					Text:       chunk.Text,
					AudioData:  chunk.Samples,
					SampleRate: int(chunk.SampleRate),
					State:      "chunk",
				})
			}

			if playLocal && s.speaking.Load() && ctx.Err() == nil {
				if err := audio.PlayFloat32(chunk.Samples, chunk.SampleRate); err != nil {
					return fmt.Errorf("serve: tts playback: %w", err)
				}
			}
		}
	}

	if pub != nil {
		if interrupted {
			pub.Publish(event.TTSAudioEvent{
				SampleRate: lastSampleRate,
				State:      "interrupted",
			})
		} else {
			pub.Publish(event.TTSAudioEvent{
				Text:       text,
				SampleRate: lastSampleRate,
				State:      "end",
			})
		}
	}

	if interrupted && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// SpeakStream consumes text tokens from textCh as they are generated (e.g. by an LLM),
// feeds them into the streaming TTS synthesizer, and streams synthesized audio chunks
// concurrently to publishers and local playback.
func (s *TTSSpeaker) SpeakStream(ctx context.Context, pub Publisher, textCh <-chan string, voice string, speed float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	synth, err := s.synthesizer(voice, speed)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if pub == nil {
		pub = s.publisher
	}
	playLocal := s.playLocal
	s.mu.Unlock()

	s.speaking.Store(true)
	defer s.speaking.Store(false)

	if pub != nil {
		pub.Publish(event.TTSAudioEvent{
			State: "start",
		})
	}

	stream := synth.NewStream()

	pushDone := make(chan struct{})
	go func() {
		defer close(pushDone)
		for {
			select {
			case <-ctx.Done():
				_ = stream.Cancel()
				return
			case token, ok := <-textCh:
				if !ok {
					_ = stream.EndInput()
					return
				}
				if token != "" {
					if err := stream.PushText(token); err != nil {
						return
					}
				}
			}
		}
	}()

	var lastSampleRate int
	interrupted := false

	for {
		if ctx.Err() != nil || !s.speaking.Load() {
			interrupted = true
			_ = stream.Cancel()
			break
		}

		chunk, err := stream.NextChunk()
		if err == moonshine.ErrEndOfStream {
			break
		}
		if err == moonshine.ErrCancelled {
			interrupted = true
			break
		}
		if err == moonshine.ErrNeedText {
			select {
			case <-pushDone:
				_ = stream.Flush()
			case <-time.After(10 * time.Millisecond):
			case <-ctx.Done():
				interrupted = true
				_ = stream.Cancel()
				break
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("serve: tts next chunk: %w", err)
		}

		if chunk != nil && len(chunk.Samples) > 0 {
			lastSampleRate = int(chunk.SampleRate)
			if pub != nil {
				pub.Publish(event.TTSAudioEvent{
					Text:       chunk.Text,
					AudioData:  chunk.Samples,
					SampleRate: int(chunk.SampleRate),
					State:      "chunk",
				})
			}

			if playLocal && s.speaking.Load() && ctx.Err() == nil {
				if err := audio.PlayFloat32(chunk.Samples, chunk.SampleRate); err != nil {
					return fmt.Errorf("serve: tts playback: %w", err)
				}
			}
		}
	}

	if pub != nil {
		if interrupted {
			pub.Publish(event.TTSAudioEvent{
				SampleRate: lastSampleRate,
				State:      "interrupted",
			})
		} else {
			pub.Publish(event.TTSAudioEvent{
				SampleRate: lastSampleRate,
				State:      "end",
			})
		}
	}

	if interrupted && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// Interrupt stops active speech indicator, cancels in-flight TTS generation in C++,
// and emits an in-protocol "interrupted" event.
func (s *TTSSpeaker) Interrupt(ctx context.Context) {
	s.speaking.Store(false)
	s.mu.Lock()
	for _, syn := range s.synths {
		_ = syn.Cancel()
	}
	pub := s.publisher
	s.mu.Unlock()

	if pub != nil {
		pub.Publish(event.TTSAudioEvent{
			State: "interrupted",
		})
	}
}

// Speaking reports whether a Speak call is currently synthesizing or
// playing audio. See TTSSpeaker's doc comment for the barge-in guard this
// exists to support.
func (s *TTSSpeaker) Speaking() bool { return s.speaking.Load() }

// Close releases any created synthesizers. Safe to call even if Speak was never called.
func (s *TTSSpeaker) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var firstErr error
	for _, synth := range s.synths {
		if err := synth.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.synths = nil
	return firstErr
}

func (s *TTSSpeaker) synthesizer(voice string, speed float64) (*moonshine.Synthesizer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("serve: tts speaker is closed")
	}
	key := fmt.Sprintf("%s:%g", voice, speed)
	if synth, ok := s.synths[key]; ok {
		return synth, nil
	}

	opts := make([]moonshine.Option, len(s.baseOpts))
	copy(opts, s.baseOpts)
	if voice != "" {
		opts = append(opts, moonshine.Option{Name: "voice", Value: voice})
	}
	if speed > 0 {
		opts = append(opts, moonshine.Option{Name: "speed", Value: fmt.Sprintf("%g", speed)})
	}

	synth, err := moonshine.NewSynthesizer(s.language, opts...)
	if err != nil {
		return nil, fmt.Errorf("serve: creating tts synthesizer: %w", err)
	}
	s.synths[key] = synth
	return synth, nil
}

var _ Speaker = (*TTSSpeaker)(nil)
