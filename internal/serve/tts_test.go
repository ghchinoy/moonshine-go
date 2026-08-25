package serve

import (
	"context"
	"testing"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

// These tests deliberately avoid calling Speak (which requires libmoonshine
// to be loaded, real audio output, etc. -- see pkg/moonshine/smoke_test.go
// for that coverage). They only exercise the lazy-construction and
// lifecycle paths that must work without any native library loaded, per
// make test's native-free requirement (AGENTS.md).

func TestTTSSpeaker_SpeakingDefaultsFalse(t *testing.T) {
	s := NewTTSSpeaker("en_us")
	if s.Speaking() {
		t.Error("Speaking() = true before any Speak call, want false")
	}
}

func TestTTSSpeaker_CloseWithoutSpeak(t *testing.T) {
	s := NewTTSSpeaker("en_us")
	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil (no synthesizer was ever created)", err)
	}
	// Safe to call twice.
	if err := s.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil", err)
	}
}

func TestTTSSpeaker_SetPublisherAndInterrupt(t *testing.T) {
	s := NewTTSSpeaker("en_us")
	pub := &fakePublisher{}
	s.SetPublisher(pub, false)

	ctx := context.Background()
	s.Interrupt(ctx)

	if len(pub.published) != 1 {
		t.Fatalf("published len = %d, want 1", len(pub.published))
	}
	te, ok := pub.published[0].(event.TTSAudioEvent)
	if !ok || te.State != "interrupted" {
		t.Fatalf("got %#v, want TTSAudioEvent{State: \"interrupted\"}", pub.published[0])
	}
}

func TestTTSSpeaker_Speak_CancelledContext(t *testing.T) {
	s := NewTTSSpeaker("en_us")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.Speak(ctx, nil, "test", "", 1.0)
	if err != context.Canceled {
		t.Errorf("Speak with cancelled context = %v, want %v", err, context.Canceled)
	}
}

func TestTTSSpeaker_SpeakStream_CancelledContext(t *testing.T) {
	s := NewTTSSpeaker("en_us")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	textCh := make(chan string)
	close(textCh)

	err := s.SpeakStream(ctx, nil, textCh, "", 1.0)
	if err != context.Canceled {
		t.Errorf("SpeakStream with cancelled context = %v, want %v", err, context.Canceled)
	}
}

func TestTTSSpeaker_Closed(t *testing.T) {
	s := NewTTSSpeaker("en_us")
	_ = s.Close()

	if err := s.Speak(context.Background(), nil, "test", "", 1.0); err == nil {
		t.Error("Speak on closed TTSSpeaker should return error")
	}

	textCh := make(chan string)
	close(textCh)
	if err := s.SpeakStream(context.Background(), nil, textCh, "", 1.0); err == nil {
		t.Error("SpeakStream on closed TTSSpeaker should return error")
	}
}

type fakeStreamSpeaker struct {
	lastText   string
	lastTokens []string
}

func (f *fakeStreamSpeaker) Speak(_ context.Context, _ Publisher, text, _ string, _ float64) error {
	f.lastText = text
	return nil
}

func (f *fakeStreamSpeaker) Speaking() bool { return false }

func (f *fakeStreamSpeaker) SpeakStream(_ context.Context, _ Publisher, textCh <-chan string, _ string, _ float64) error {
	for tok := range textCh {
		f.lastTokens = append(f.lastTokens, tok)
	}
	return nil
}

func TestScopedSpeaker_SpeakStream(t *testing.T) {
	fake := &fakeStreamSpeaker{}
	scoped := &scopedSpeaker{base: fake}

	textCh := make(chan string, 3)
	textCh <- "hello "
	textCh <- "world"
	close(textCh)

	if err := scoped.SpeakStream(context.Background(), nil, textCh, "", 1.0); err != nil {
		t.Fatalf("SpeakStream: %v", err)
	}

	if len(fake.lastTokens) != 2 || fake.lastTokens[0] != "hello " || fake.lastTokens[1] != "world" {
		t.Errorf("unexpected tokens received: %v", fake.lastTokens)
	}
}
