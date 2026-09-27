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

func TestDomainCustomizationPauseStandbyActions(t *testing.T) {
	sink := &mockActionSink{}
	handler := newDomainAgentFlow(sink)
	ctx := context.Background()

	// 1. Domain switch works
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "switch to cloud"})
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

	// 2. Pause the session: emits speak + session.pause with PauseArgs.Passthrough
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "stop listening"})

	actions := sink.getActions()
	var pauseFound bool
	for _, a := range actions {
		if a.Verb == "session.pause" {
			var args serveapi.PauseArgs
			if err := json.Unmarshal(a.Args, &args); err == nil && len(args.Passthrough) > 0 {
				if args.Passthrough[0] == "resume listening" && args.Passthrough[1] == "start listening" {
					pauseFound = true
					break
				}
			}
		}
	}
	if !pauseFound {
		t.Fatal("expected session.pause with PauseArgs containing resume listening / start listening")
	}

	// 3. Resume the session: emits session.resume + speak confirmation
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "start listening"})

	actionsAfter := sink.getActions()
	var resumeFound bool
	for _, a := range actionsAfter {
		if a.Verb == "session.resume" {
			resumeFound = true
			break
		}
	}
	if !resumeFound {
		t.Fatal("expected session.resume action")
	}

	// 4. Clinical domain switch after resume
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "switch to clinical domain"})
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
		t.Fatal("expected session.set_keyterms with Atorvastatin for clinical domain switch")
	}
}
