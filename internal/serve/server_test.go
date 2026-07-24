package serve

import (
	"context"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/session"
)

type fakeAudioSource struct {
	ch     chan []float32
	err    error
	muted  func() bool
	closed bool
}

func (f *fakeAudioSource) Chunks() <-chan []float32 { return f.ch }
func (f *fakeAudioSource) Err() error               { return f.err }
func (f *fakeAudioSource) SetMutedFunc(fn func() bool) {
	f.muted = fn
}

type fakeLiveSession struct {
	updates chan session.Update
}

func (f *fakeLiveSession) Run(ctx context.Context) {
	<-ctx.Done()
}

func (f *fakeLiveSession) Updates() <-chan session.Update {
	return f.updates
}

func TestServer_NewServer_Validation(t *testing.T) {
	source := &fakeAudioSource{ch: make(chan []float32)}

	// Missing Transcriber/Session
	_, err := NewServer(ServerConfig{AudioSource: source})
	if err == nil {
		t.Error("expected error for missing Transcriber/Session, got nil")
	}

	// Missing AudioSource/Session
	_, err = NewServer(ServerConfig{Transcriber: &moonshine.Transcriber{}})
	if err == nil {
		t.Error("expected error for missing AudioSource/Session, got nil")
	}

	// Valid config with Transcriber + AudioSource
	srv, err := NewServer(ServerConfig{
		Transcriber: &moonshine.Transcriber{},
		AudioSource: source,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv.Hub() == nil {
		t.Error("expected non-nil Hub, got nil")
	}

	// Valid config with custom Session
	fakeSess := &fakeLiveSession{updates: make(chan session.Update)}
	srv2, err := NewServer(ServerConfig{
		Session: fakeSess,
	})
	if err != nil {
		t.Fatalf("unexpected error with custom Session: %v", err)
	}
	if srv2.Hub() == nil {
		t.Error("expected non-nil Hub, got nil")
	}
}

func TestServer_Run_Lifecycle(t *testing.T) {
	ch := make(chan []float32, 1)
	source := &fakeAudioSource{ch: ch}
	fakeSess := &fakeLiveSession{updates: make(chan session.Update)}

	srv, err := NewServer(ServerConfig{
		AudioSource:  source,
		Session:      fakeSess,
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()

	// Give srv.Run a moment to initialize
	time.Sleep(10 * time.Millisecond)

	// Verify muter hook was attached
	if source.muted == nil {
		t.Error("expected SetMutedFunc to be called on AudioSource, got nil")
	}

	// Cancel context to stop server
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Server.Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Server.Run did not stop within 1 second after context cancellation")
	}
}
