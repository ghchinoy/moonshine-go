package serve_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

func TestIntentMatcher_DefaultRules(t *testing.T) {
	matcher := serve.NewIntentMatcher()
	ctx := context.Background()

	tests := []struct {
		name         string
		utterance    string
		wantActions  bool
		wantVerb     string
		wantSpeakTxt string
	}{
		{
			name:        "pause listening",
			utterance:   "stop listening.",
			wantActions: true,
			wantVerb:    "session.pause",
		},
		{
			name:        "pause listening alternate",
			utterance:   "PAUSE LISTENING",
			wantActions: true,
			wantVerb:    "session.pause",
		},
		{
			name:        "resume listening",
			utterance:   "resume listening",
			wantActions: true,
			wantVerb:    "session.resume",
		},
		{
			name:        "stop session",
			utterance:   "stop sidecar.",
			wantActions: true,
			wantVerb:    "session.stop",
		},
		{
			name:         "say phrase",
			utterance:    "Say Hello world.",
			wantActions:  true,
			wantVerb:     "speak",
			wantSpeakTxt: "Hello world",
		},
		{
			name:        "unmatched query",
			utterance:   "What is the weather in Tokyo?",
			wantActions: false,
		},
		{
			name:        "empty utterance",
			utterance:   "   ",
			wantActions: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := moonshine.Line{ID: 1, Text: tt.utterance, IsComplete: true}
			actions := matcher.OnFinalizedLine(ctx, line)

			if !tt.wantActions {
				if len(actions) > 0 {
					t.Fatalf("expected no actions, got %v", actions)
				}
				return
			}

			if len(actions) == 0 {
				t.Fatalf("expected actions for %q, got none", tt.utterance)
			}

			if actions[0].Verb != tt.wantVerb {
				t.Errorf("expected verb %q, got %q", tt.wantVerb, actions[0].Verb)
			}

			if tt.wantSpeakTxt != "" {
				var speakArgs event.SpeakArgs
				if err := json.Unmarshal(actions[0].Args, &speakArgs); err != nil {
					t.Fatalf("failed to unmarshal speak args: %v", err)
				}
				if speakArgs.Text != tt.wantSpeakTxt {
					t.Errorf("expected speak text %q, got %q", tt.wantSpeakTxt, speakArgs.Text)
				}
			}
		})
	}
}
