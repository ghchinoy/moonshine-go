package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

type mockActionSink struct {
	mu      sync.Mutex
	actions []serveapi.ActionRequest
}

func (m *mockActionSink) Dispatch(ctx context.Context, req serveapi.ActionRequest) (serveapi.ActionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actions = append(m.actions, req)
	return serveapi.ActionResult{ID: req.ID, OK: true}, nil
}

func (m *mockActionSink) getActions() []serveapi.ActionRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]serveapi.ActionRequest, len(m.actions))
	copy(out, m.actions)
	return out
}

func TestDomainCustomizationPauseResume(t *testing.T) {
	sink := &mockActionSink{}
	state := &SessionState{}
	handler := newDomainAgentFlow(sink, state)
	pausedHandler := &pausedAgentHandler{
		inner: handler,
		state: state,
		debug: true,
	}

	ctx := context.Background()

	// 1. Initially unpaused: domain switch works
	pausedHandler.OnFinalizedLine(ctx, serveapi.Line{Text: "switch to cloud"})
	var foundCloud bool
	for i := 0; i < 20; i++ {
		time.Sleep(10 * time.Millisecond)
		for _, a := range sink.getActions() {
			if a.Verb == "session.set_keyterms" {
				var kt serveapi.SetKeytermsArgs
				_ = json.Unmarshal(a.Args, &kt)
				for _, term := range kt.Keyterms {
					if term == "Kubernetes" {
						foundCloud = true
						break
					}
				}
			}
		}
		if foundCloud {
			break
		}
	}
	if !foundCloud {
		t.Fatal("expected session.set_keyterms with Kubernetes for 'switch to cloud'")
	}

	// 2. Pause the session via voice command
	pausedHandler.OnFinalizedLine(ctx, serveapi.Line{Text: "stop listening"})
	if !state.IsPaused() {
		t.Fatal("expected session to be paused after 'stop listening'")
	}

	// 3. While paused: domain switch attempt is ignored
	actionCountBefore := len(sink.getActions())
	pausedHandler.OnFinalizedLine(ctx, serveapi.Line{Text: "switch to medical"})
	time.Sleep(50 * time.Millisecond)
	actionCountAfter := len(sink.getActions())
	if actionCountAfter != actionCountBefore {
		t.Fatalf("expected no actions emitted while paused, got %d", actionCountAfter-actionCountBefore)
	}

	// 4. Resume via voice command
	pausedHandler.OnFinalizedLine(ctx, serveapi.Line{Text: "resume listening"})
	if state.IsPaused() {
		t.Fatal("expected session to be unpaused after 'resume listening'")
	}

	// 5. After unpause: domain switch works again
	pausedHandler.OnFinalizedLine(ctx, serveapi.Line{Text: "switch to medical"})
	var foundMedical bool
	for i := 0; i < 20; i++ {
		time.Sleep(10 * time.Millisecond)
		for _, a := range sink.getActions() {
			if a.Verb == "session.set_keyterms" {
				var kt serveapi.SetKeytermsArgs
				_ = json.Unmarshal(a.Args, &kt)
				for _, term := range kt.Keyterms {
					if term == "Atorvastatin" {
						foundMedical = true
						break
					}
				}
			}
		}
		if foundMedical {
			break
		}
	}
	if !foundMedical {
		t.Fatal("expected session.set_keyterms with Atorvastatin after resume")
	}
}
