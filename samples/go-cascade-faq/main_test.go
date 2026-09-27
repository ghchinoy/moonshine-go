package main

import (
	"context"
	"encoding/json"
	"strings"
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

func TestAgentFlowPauseStandbyActions(t *testing.T) {
	sink := &mockActionSink{}
	handler := newAgentFlow(sink, nil, 0)
	ctx := context.Background()

	// 1. "stop listening" triggers speak confirmation and native session.pause action with passthrough
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "stop listening"})

	actions := sink.getActions()
	if len(actions) < 2 {
		t.Fatalf("expected at least 2 actions (speak + session.pause), got %d: %v", len(actions), actions)
	}

	// Verify speak action
	var speakArgs serveapi.SpeakArgs
	_ = json.Unmarshal(actions[0].Args, &speakArgs)
	if !strings.Contains(speakArgs.Text, "Listening paused") {
		t.Errorf("expected 'Listening paused' in speak action, got %q", speakArgs.Text)
	}

	// Verify session.pause action with wake phrases in PauseArgs
	pauseAction := actions[1]
	if pauseAction.Verb != "session.pause" {
		t.Errorf("expected verb 'session.pause', got %q", pauseAction.Verb)
	}
	var pauseArgs serveapi.PauseArgs
	if err := json.Unmarshal(pauseAction.Args, &pauseArgs); err != nil {
		t.Fatalf("unmarshaling PauseArgs: %v", err)
	}
	if len(pauseArgs.Passthrough) != 2 || pauseArgs.Passthrough[0] != "resume listening" || pauseArgs.Passthrough[1] != "start listening" {
		t.Errorf("expected passthrough wake phrases ['resume listening', 'start listening'], got %v", pauseArgs.Passthrough)
	}

	// 2. "start listening" triggers session.resume and speak confirmation
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "start listening"})

	actionsAfter := sink.getActions()
	if len(actionsAfter) < 4 {
		t.Fatalf("expected 4 actions after resume, got %d", len(actionsAfter))
	}

	resumeAction := actionsAfter[2]
	if resumeAction.Verb != "session.resume" {
		t.Errorf("expected verb 'session.resume', got %q", resumeAction.Verb)
	}

	_ = json.Unmarshal(actionsAfter[3].Args, &speakArgs)
	if !strings.Contains(speakArgs.Text, "Listening resumed") {
		t.Errorf("expected 'Listening resumed' in speak action, got %q", speakArgs.Text)
	}

	// 3. FAQ trigger "mission" triggers async answer
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "mission"})
	var foundMission bool
	for i := 0; i < 20; i++ {
		time.Sleep(10 * time.Millisecond)
		for _, a := range sink.getActions() {
			if a.Verb == "speak" {
				var sa serveapi.SpeakArgs
				_ = json.Unmarshal(a.Args, &sa)
				if strings.Contains(sa.Text, "bringing back the classic voice cascade") {
					foundMission = true
					break
				}
			}
		}
		if foundMission {
			break
		}
	}
	if !foundMission {
		t.Errorf("expected mission FAQ answer spoken")
	}
}
