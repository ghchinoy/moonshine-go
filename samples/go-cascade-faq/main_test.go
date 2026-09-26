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

type mockAgentHandler struct {
	mu    sync.Mutex
	lines []string
}

func (m *mockAgentHandler) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = append(m.lines, line.Text)
	return nil
}

func (m *mockAgentHandler) getLines() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.lines))
	copy(out, m.lines)
	return out
}

func TestSessionState(t *testing.T) {
	state := &SessionState{}
	if state.IsPaused() {
		t.Fatal("expected newly created SessionState to be unpaused")
	}

	state.SetPaused(true)
	if !state.IsPaused() {
		t.Fatal("expected state to be paused")
	}

	state.SetPaused(false)
	if state.IsPaused() {
		t.Fatal("expected state to be unpaused")
	}
}

func TestPausedAgentHandlerFiltering(t *testing.T) {
	mock := &mockAgentHandler{}
	state := &SessionState{}
	handler := &pausedAgentHandler{
		inner: mock,
		state: state,
		debug: true,
	}

	ctx := context.Background()

	// 1. Unpaused: normal line forwarded
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "what is the mission?"})
	lines := mock.getLines()
	if len(lines) != 1 || lines[0] != "what is the mission?" {
		t.Fatalf("expected 1 line forwarded, got %v", lines)
	}

	// 2. Pause the session
	state.SetPaused(true)

	// 3. Paused: normal line discarded
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "what is the mission?"})
	lines = mock.getLines()
	if len(lines) != 1 {
		t.Fatalf("expected line to be ignored while paused, got %v", lines)
	}

	// 4. Paused: voice resume trigger unpauses and is forwarded
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "resume listening"})
	if state.IsPaused() {
		t.Fatal("expected handler to unpause on 'resume listening'")
	}
	lines = mock.getLines()
	if len(lines) != 2 || lines[1] != "resume listening" {
		t.Fatalf("expected resume line forwarded, got %v", lines)
	}

	// 5. Unpaused: subsequent normal line forwarded again
	handler.OnFinalizedLine(ctx, serveapi.Line{Text: "tell me about privacy"})
	lines = mock.getLines()
	if len(lines) != 3 || lines[2] != "tell me about privacy" {
		t.Fatalf("expected post-unpause line forwarded, got %v", lines)
	}
}

func TestAgentFlowPauseResumeIntegration(t *testing.T) {
	sink := &mockActionSink{}
	state := &SessionState{}
	handler := newAgentFlow(sink, nil, 0, state)
	pausedHandler := &pausedAgentHandler{
		inner: handler,
		state: state,
		debug: true,
	}

	ctx := context.Background()

	// Utterance 1: "stop listening" pauses session and speaks confirmation
	pausedHandler.OnFinalizedLine(ctx, serveapi.Line{Text: "stop listening"})
	if !state.IsPaused() {
		t.Fatal("expected session to be paused after 'stop listening'")
	}

	actions := sink.getActions()
	if len(actions) == 0 {
		t.Fatal("expected speak action for pause confirmation")
	}
	var speakArgs serveapi.SpeakArgs
	_ = json.Unmarshal(actions[len(actions)-1].Args, &speakArgs)
	if !strings.Contains(speakArgs.Text, "Listening paused") {
		t.Fatalf("expected 'Listening paused' in speak action, got %q", speakArgs.Text)
	}

	// Utterance 2: "what is the mission?" while paused should be dropped
	actionCountBefore := len(sink.getActions())
	pausedHandler.OnFinalizedLine(ctx, serveapi.Line{Text: "mission"})
	actionCountAfter := len(sink.getActions())
	if actionCountAfter != actionCountBefore {
		t.Fatalf("expected no actions emitted while paused, got %d actions", actionCountAfter-actionCountBefore)
	}

	// Utterance 3: "resume listening" unpauses and speaks confirmation
	pausedHandler.OnFinalizedLine(ctx, serveapi.Line{Text: "resume listening"})
	if state.IsPaused() {
		t.Fatal("expected session to be unpaused after 'resume listening'")
	}

	actions = sink.getActions()
	_ = json.Unmarshal(actions[len(actions)-1].Args, &speakArgs)
	if !strings.Contains(speakArgs.Text, "Listening resumed") {
		t.Fatalf("expected 'Listening resumed' in speak action, got %q", speakArgs.Text)
	}

	// Utterance 4: "mission" while unpaused triggers FAQ answer (flow runs asynchronously in goroutine)
	pausedHandler.OnFinalizedLine(ctx, serveapi.Line{Text: "mission"})
	var foundMission bool
	for i := 0; i < 20; i++ {
		time.Sleep(10 * time.Millisecond)
		actions = sink.getActions()
		_ = json.Unmarshal(actions[len(actions)-1].Args, &speakArgs)
		if strings.Contains(speakArgs.Text, "bringing back the classic voice cascade") {
			foundMission = true
			break
		}
	}
	if !foundMission {
		t.Fatalf("expected mission FAQ answer, got %q", speakArgs.Text)
	}
}
