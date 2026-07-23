package serve

import "testing"

// These tests deliberately avoid calling Speak (which requires libmoonshine
// to be loaded, real audio output, etc. -- see internal/moonshine/smoke_test.go
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
