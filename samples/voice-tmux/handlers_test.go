package main

import (
	"context"
	"testing"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

func TestControlHandlerVerbs(t *testing.T) {
	tmux := &TmuxClient{DryRun: true}
	state := &SessionState{}
	ctrl := &controlHandler{tmux: tmux, state: state, debug: true}

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
		{"stop listening", "none"},
		{"pause listening", "none"},
		{"resume listening", "none"},
		{"start listening", "none"},
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

func TestSessionPauseResumeFlow(t *testing.T) {
	tmux := &TmuxClient{DryRun: true}
	state := &SessionState{}
	ctrl := &controlHandler{tmux: tmux, state: state}
	dict := &dictationHandler{tmux: tmux, state: state, enableAliases: true}
	composite := serveapi.NewCompositeHandler(ctrl, dict)

	ctx := context.Background()

	// 1. Initial state: not paused. Dictation works.
	if state.IsPaused() {
		t.Fatal("expected initially unpaused")
	}
	actions := composite.OnFinalizedLine(ctx, serveapi.Line{Text: "git status"})
	if len(actions) != 0 {
		t.Errorf("expected empty actions for dictation, got %#v", actions)
	}

	// 2. Pause session via voice
	actions = composite.OnFinalizedLine(ctx, serveapi.Line{Text: "pause listening"})
	if !state.IsPaused() {
		t.Fatal("expected state to be paused after 'pause listening'")
	}
	if len(actions) == 0 || actions[0].Verb != "none" {
		t.Errorf("expected sentinel 'none' action, got %#v", actions)
	}

	// 3. While paused: dictation is ignored
	actions = composite.OnFinalizedLine(ctx, serveapi.Line{Text: "git status"})
	if len(actions) == 0 || actions[0].Verb != "none" {
		t.Errorf("expected controlHandler to intercept while paused with sentinel 'none', got %#v", actions)
	}

	// 4. While paused: control command is ignored
	actions = composite.OnFinalizedLine(ctx, serveapi.Line{Text: "split right"})
	if len(actions) == 0 || actions[0].Verb != "none" {
		t.Errorf("expected controlHandler to intercept while paused with sentinel 'none', got %#v", actions)
	}

	// 5. Resume listening via voice
	actions = composite.OnFinalizedLine(ctx, serveapi.Line{Text: "resume listening"})
	if state.IsPaused() {
		t.Fatal("expected state to be unpaused after 'resume listening'")
	}
	if len(actions) == 0 || actions[0].Verb != "none" {
		t.Errorf("expected sentinel 'none' action, got %#v", actions)
	}

	// 6. After resume: dictation works again
	actions = composite.OnFinalizedLine(ctx, serveapi.Line{Text: "git status"})
	if len(actions) != 0 {
		t.Errorf("expected empty actions for dictation after resume, got %#v", actions)
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
		input         string
		raw           bool
		enableAliases bool
		want          string
	}{
		// Punctuation stripping + initial lowercasing
		{"Git status.", false, true, "git status"},
		{"List dash la.", false, true, "list dash la"},
		{"Docker compose up!", false, true, "docker compose up"},
		{"Where is the file?", false, true, "where is the file"},
		{"Make test;", false, true, "make test"},
		{"Echo hello,", false, true, "echo hello"},

		// Command prefix aliases (get -> git, cubectal -> kubectl, nnmm -> npm)
		{"Get status.", false, true, "git status"},
		{"Get commit -m 'init'.", false, true, "git commit -m 'init'"},
		{"Get diff HEAD.", false, true, "git diff HEAD"},
		{"Cubectal get pods.", false, true, "kubectl get pods"},
		{"Cube control get nodes.", false, true, "kubectl get nodes"},
		{"Cube cuddle logs.", false, true, "kubectl logs"},
		{"NNMM install express.", false, true, "npm install express"},
		{"Nmm run build.", false, true, "npm run build"},

		// Aliases only match at start of line
		{"I want to get status.", false, true, "i want to get status"},
		{"Please get commit info.", false, true, "please get commit info"},

		// Non-git words starting with 'get' are untouched
		{"Get a cup of coffee.", false, true, "get a cup of coffee"},
		{"Get ready.", false, true, "get ready"},

		// Aliases disabled (enableAliases = false)
		{"Get status.", false, false, "get status"},
		{"Cubectal get pods.", false, false, "cubectal get pods"},

		// Acronym preservation
		{"NPM install express.", false, true, "NPM install express"},
		{"K8S get pods.", false, true, "K8S get pods"},
		{"AWS s3 ls.", false, true, "AWS s3 ls"},

		// Raw mode (preserves formatting and trailing punctuation)
		{"Git status.", true, true, "Git status."},
		{"Hello, world!", true, true, "Hello, world!"},

		// Empty/whitespace cases
		{"   ", false, true, ""},
		{"...", false, true, ""},
	}

	for _, tt := range tests {
		got := normalizeDictation(tt.input, tt.raw, tt.enableAliases, false)
		if got != tt.want {
			t.Errorf("normalizeDictation(%q, %v, %v) = %q, want %q", tt.input, tt.raw, tt.enableAliases, got, tt.want)
		}
	}
}
