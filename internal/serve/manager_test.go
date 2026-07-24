package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

type fakeManagerSpeaker struct {
	speaking bool
	spoken   []string
}

func (f *fakeManagerSpeaker) Speak(ctx context.Context, text, voice string, speed float64) error {
	f.spoken = append(f.spoken, text)
	return nil
}

func (f *fakeManagerSpeaker) Speaking() bool {
	return f.speaking
}

func TestSessionManager_MaxSessionsEnforcement(t *testing.T) {
	mgr := NewSessionManager(SessionManagerConfig{
		MaxSessions: 2,
	})

	source1 := &fakeAudioSource{ch: make(chan []float32)}
	source2 := &fakeAudioSource{ch: make(chan []float32)}
	source3 := &fakeAudioSource{ch: make(chan []float32)}

	ctx := context.Background()

	sess1, err := mgr.CreateSession(ctx, source1)
	if err != nil {
		t.Fatalf("unexpected error creating sess1: %v", err)
	}
	if mgr.ActiveSessions() != 1 {
		t.Errorf("expected 1 active session, got %d", mgr.ActiveSessions())
	}

	sess2, err := mgr.CreateSession(ctx, source2)
	if err != nil {
		t.Fatalf("unexpected error creating sess2: %v", err)
	}
	if mgr.ActiveSessions() != 2 {
		t.Errorf("expected 2 active sessions, got %d", mgr.ActiveSessions())
	}

	// 3rd session must be rejected with ErrSessionLimitReached
	_, err = mgr.CreateSession(ctx, source3)
	if !errors.Is(err, ErrSessionLimitReached) {
		t.Fatalf("expected ErrSessionLimitReached, got %v", err)
	}

	// Close sess1, releasing a slot
	if err := sess1.Close(); err != nil {
		t.Fatalf("unexpected error closing sess1: %v", err)
	}
	if mgr.ActiveSessions() != 1 {
		t.Errorf("expected 1 active session after closing sess1, got %d", mgr.ActiveSessions())
	}

	// Now 3rd session creation should succeed
	sess3, err := mgr.CreateSession(ctx, source3)
	if err != nil {
		t.Fatalf("unexpected error creating sess3 after releasing slot: %v", err)
	}
	if mgr.ActiveSessions() != 2 {
		t.Errorf("expected 2 active sessions, got %d", mgr.ActiveSessions())
	}

	// Clean up remaining
	_ = sess2.Close()
	_ = sess3.Close()
	if mgr.ActiveSessions() != 0 {
		t.Errorf("expected 0 active sessions after cleanup, got %d", mgr.ActiveSessions())
	}
}

func TestSessionManager_SessionIsolation(t *testing.T) {
	mgr := NewSessionManager(SessionManagerConfig{
		MaxSessions: 5,
	})

	ctx := context.Background()

	source1 := &fakeAudioSource{ch: make(chan []float32)}
	source2 := &fakeAudioSource{ch: make(chan []float32)}

	sess1, err := mgr.CreateSession(ctx, source1)
	if err != nil {
		t.Fatalf("failed to create sess1: %v", err)
	}
	defer sess1.Close()

	sess2, err := mgr.CreateSession(ctx, source2)
	if err != nil {
		t.Fatalf("failed to create sess2: %v", err)
	}
	defer sess2.Close()

	// Subscribe to session 1 Hub and session 2 Hub
	_, sub1 := sess1.Hub().Subscribe()
	_, sub2 := sess2.Hub().Subscribe()

	// Publish card to sess1 Hub
	card1 := event.DisplayCard{Title: "Card for Session 1"}
	sess1.Hub().Publish(card1)

	select {
	case ev := <-sub1:
		card, ok := ev.(event.DisplayCard)
		if !ok || card.Title != "Card for Session 1" {
			t.Errorf("expected card for session 1, got %v", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for event on session 1")
	}

	// Verify session 2 Hub received nothing
	select {
	case ev := <-sub2:
		t.Errorf("unexpected event received on session 2: %v", ev)
	default:
		// Expected: session 2 is isolated
	}
}

func TestSessionManager_ScopedSpeakerBargeIn(t *testing.T) {
	baseSpk := &fakeManagerSpeaker{}
	mgr := NewSessionManager(SessionManagerConfig{
		Speaker:      baseSpk,
		AllowActions: true,
	})

	ctx := context.Background()
	source1 := &fakeAudioSource{ch: make(chan []float32)}
	source2 := &fakeAudioSource{ch: make(chan []float32)}

	sess1, err := mgr.CreateSession(ctx, source1)
	if err != nil {
		t.Fatalf("failed to create sess1: %v", err)
	}
	defer sess1.Close()

	sess2, err := mgr.CreateSession(ctx, source2)
	if err != nil {
		t.Fatalf("failed to create sess2: %v", err)
	}
	defer sess2.Close()

	// Before speaking: neither scoped speaker is speaking
	if sess1.spk.Speaking() {
		t.Error("expected sess1 speaker.Speaking() = false")
	}
	if sess2.spk.Speaking() {
		t.Error("expected sess2 speaker.Speaking() = false")
	}

	// Verify muter hook was attached to both sources
	if source1.muted == nil || source2.muted == nil {
		t.Fatal("expected SetMutedFunc to be set on both sources")
	}

	// Trigger speak on sess1's dispatcher
	req := event.ActionRequest{
		ID:   "req-1",
		Verb: "speak",
		Args: []byte(`{"text":"hello session 1"}`),
	}
	res := sess1.Dispatcher().Handle(ctx, req)
	if !res.OK {
		t.Fatalf("speak handle failed: %s", res.Err)
	}

	if len(baseSpk.spoken) != 1 || baseSpk.spoken[0] != "hello session 1" {
		t.Errorf("expected base speaker to have spoken 'hello session 1', got %v", baseSpk.spoken)
	}
}

func TestSessionManager_CloseAll(t *testing.T) {
	mgr := NewSessionManager(SessionManagerConfig{
		MaxSessions: 10,
	})

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		source := &fakeAudioSource{ch: make(chan []float32)}
		_, err := mgr.CreateSession(ctx, source)
		if err != nil {
			t.Fatalf("failed to create session %d: %v", i, err)
		}
	}

	if mgr.ActiveSessions() != 3 {
		t.Errorf("expected 3 active sessions, got %d", mgr.ActiveSessions())
	}

	if err := mgr.Close(); err != nil {
		t.Fatalf("failed to close manager: %v", err)
	}

	if mgr.ActiveSessions() != 0 {
		t.Errorf("expected 0 active sessions after Close(), got %d", mgr.ActiveSessions())
	}
}
