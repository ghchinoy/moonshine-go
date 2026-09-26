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

func TestNormalizeDictation(t *testing.T) {
	tests := []struct {
		input string
		raw   bool
		want  string
	}{
		// Punctuation stripping + initial lowercasing
		{"Git status.", false, "git status"},
		{"List dash la.", false, "list dash la"},
		{"Docker compose up!", false, "docker compose up"},
		{"Where is the file?", false, "where is the file"},
		{"Make test;", false, "make test"},
		{"Echo hello,", false, "echo hello"},

		// Acronym preservation
		{"NPM install express.", false, "NPM install express"},
		{"K8S get pods.", false, "K8S get pods"},
		{"AWS s3 ls.", false, "AWS s3 ls"},

		// Raw mode (preserves formatting and trailing punctuation)
		{"Git status.", true, "Git status."},
		{"Hello, world!", true, "Hello, world!"},

		// Empty/whitespace cases
		{"   ", false, ""},
		{"...", false, ""},
	}

	for _, tt := range tests {
		got := normalizeDictation(tt.input, tt.raw)
		if got != tt.want {
			t.Errorf("normalizeDictation(%q, %v) = %q, want %q", tt.input, tt.raw, got, tt.want)
		}
	}
}
