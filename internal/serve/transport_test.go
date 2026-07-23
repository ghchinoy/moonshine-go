package serve

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

// fakeTransport is a minimal in-memory Transport for testing Manager
// without any real network/IPC.
type fakeTransport struct {
	mu        sync.Mutex
	started   bool
	closed    bool
	startErr  error
	published []any
	actions   chan event.ActionRequest
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{actions: make(chan event.ActionRequest, 8)}
}

func (f *fakeTransport) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	return f.startErr
}

func (f *fakeTransport) Publish(ev any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, ev)
	return nil
}

func (f *fakeTransport) Actions() <-chan event.ActionRequest { return f.actions }

func (f *fakeTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	close(f.actions)
	return nil
}

// inject simulates a subscriber on this transport sending an action in.
func (f *fakeTransport) inject(req event.ActionRequest) {
	f.actions <- req
}

func (f *fakeTransport) publishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func TestManager_Publish_FansOutToAllTransports(t *testing.T) {
	t1, t2 := newFakeTransport(), newFakeTransport()
	m := NewManager(t1, t2)

	m.Publish(event.TranscriptEvent{ElapsedMs: 5})

	if got := t1.publishedCount(); got != 1 {
		t.Errorf("t1 published %d events, want 1", got)
	}
	if got := t2.publishedCount(); got != 1 {
		t.Errorf("t2 published %d events, want 1", got)
	}
}

func TestManager_Start_StartsAllTransports(t *testing.T) {
	t1, t2 := newFakeTransport(), newFakeTransport()
	m := NewManager(t1, t2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	if !t1.started || !t2.started {
		t.Errorf("t1.started=%v t2.started=%v, want both true", t1.started, t2.started)
	}
	_ = m.Close()
}

func TestManager_Start_ReturnsFirstErrorButStartsRemaining(t *testing.T) {
	failErr := errors.New("bind failed")
	t1 := newFakeTransport()
	t1.startErr = failErr
	t2 := newFakeTransport()
	m := NewManager(t1, t2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := m.Start(ctx)
	if !errors.Is(err, failErr) {
		t.Fatalf("Start() = %v, want %v", err, failErr)
	}
	if !t2.started {
		t.Error("t2 should still have been started despite t1 failing")
	}
	_ = m.Close()
}

func TestManager_Actions_MergesFromMultipleTransports(t *testing.T) {
	t1, t2 := newFakeTransport(), newFakeTransport()
	m := NewManager(t1, t2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	t1.inject(event.ActionRequest{ID: "from-t1", Verb: "speak"})
	t2.inject(event.ActionRequest{ID: "from-t2", Verb: "display"})

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case req := <-m.Actions():
			seen[req.ID] = true
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for merged action %d", i+1)
		}
	}
	if !seen["from-t1"] || !seen["from-t2"] {
		t.Fatalf("seen = %v, want both from-t1 and from-t2", seen)
	}
}

func TestManager_Close_ClosesTransportsAndActionsChannel(t *testing.T) {
	t1 := newFakeTransport()
	m := NewManager(t1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if !t1.closed {
		t.Error("underlying transport was not closed")
	}
	// Actions() channel must be closed after Manager.Close().
	select {
	case _, open := <-m.Actions():
		if open {
			t.Error("Actions() channel should be closed after Manager.Close()")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Actions() channel to close")
	}
	// Safe to call twice.
	if err := m.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil", err)
	}
}

func TestManager_NoTransports(t *testing.T) {
	m := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start() with zero transports = %v, want nil", err)
	}
	m.Publish(event.TranscriptEvent{}) // must not panic
	if err := m.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

var _ Transport = (*fakeTransport)(nil)
