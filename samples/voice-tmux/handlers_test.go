package main

import (
	"context"
	"testing"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

func TestControlHandlerVerbs(t *testing.T) {
	tmux := &TmuxClient{DryRun: true}
	ctrl := &controlHandler{tmux: tmux, debug: true}

	tests := []struct {
		input string
		want  string
	}{
		{"run it", "none"},
		{"execute", "none"},
		{"enter", "none"},
		{"interrupt", "none"},
		{"cancel", "none"},
		{"control c", "none"},
		{"clear", "none"},
		{"new window", "none"},
		{"split right", "none"},
		{"split down", "none"},
		{"next window", "none"},
		{"previous window", "none"},
		{"scroll up", "none"},
		{"scroll down", "none"},
		{"stop listening", "session.pause"},
		{"pause listening", "session.pause"},
		{"resume listening", "session.resume"},
		{"start listening", "session.resume"},
	}

	for _, tt := range tests {
		actions := ctrl.OnFinalizedLine(context.Background(), serveapi.Line{Text: tt.input})
		if len(actions) == 0 {
			t.Errorf("input %q returned no actions, expected match", tt.input)
			continue
		}
		if actions[0].Verb != tt.want {
			t.Errorf("input %q: got verb %q, want %q", tt.input, actions[0].Verb, tt.want)
		}
	}
}

func TestControlHandlerNonMatch(t *testing.T) {
	tmux := &TmuxClient{DryRun: true}
	ctrl := &controlHandler{tmux: tmux}

	actions := ctrl.OnFinalizedLine(context.Background(), serveapi.Line{Text: "git status"})
	if len(actions) != 0 {
		t.Errorf("expected nil for dictation text, got %#v", actions)
	}
}

func TestDictationHandlerConfidenceGating(t *testing.T) {
	tmux := &TmuxClient{DryRun: true}
	dict := &dictationHandler{tmux: tmux, minConfidence: 0.80, speakConfirm: true}

	// Below threshold: should return "speak" prompt and NOT type
	actions := dict.OnFinalizedLine(context.Background(), serveapi.Line{Text: "git status", Confidence: 0.60})
	if len(actions) == 0 || actions[0].Verb != "speak" {
		t.Errorf("expected speak prompt for low confidence, got %#v", actions)
	}

	// Above threshold: should return nil (typed)
	actions = dict.OnFinalizedLine(context.Background(), serveapi.Line{Text: "git status", Confidence: 0.90})
	if len(actions) != 0 {
		t.Errorf("expected nil actions on successful dictation, got %#v", actions)
	}
}
