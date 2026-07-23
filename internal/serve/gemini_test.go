package serve_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

type fakeLLMClient struct {
	turnsFunc func(history []serve.Turn) (string, []serve.ToolCall, error)
}

func (f *fakeLLMClient) GenerateWithTools(ctx context.Context, history []serve.Turn) (string, []serve.ToolCall, error) {
	if f.turnsFunc != nil {
		return f.turnsFunc(history)
	}
	return "Default response", nil, nil
}

func TestGeminiAgent_LookupAndSpeak(t *testing.T) {
	retriever := serve.NewStaticRetriever(
		serve.Result{Title: "Moonshine Docs", Snippet: "Moonshine is an ASR model.", Source: "docs"},
	)

	// Step 1: Model calls tool 'lookup'
	// Step 2: Model receives tool result and emits 'speak' + 'display_card'
	client := &fakeLLMClient{
		turnsFunc: func(history []serve.Turn) (string, []serve.ToolCall, error) {
			lastTurn := history[len(history)-1]
			if lastTurn.Role == "user" {
				args, _ := json.Marshal(map[string]string{"query": "Moonshine"})
				return "", []serve.ToolCall{
					{Name: "lookup", Args: args},
				}, nil
			}

			if lastTurn.Role == "tool" {
				for _, tr := range lastTurn.ToolResponses {
					if tr.Name == "lookup" {
						cardArgs, _ := json.Marshal(event.DisplayCard{Title: "Moonshine Docs", Body: "Moonshine is an ASR model."})
						speakArgs, _ := json.Marshal(event.SpeakArgs{Text: "I found information about Moonshine."})
						return "", []serve.ToolCall{
							{Name: "display_card", Args: cardArgs},
							{Name: "speak", Args: speakArgs},
						}, nil
					}
				}
				return "Here is what I found.", nil, nil
			}

			return "Finished.", nil, nil
		},
	}

	agent := serve.NewGeminiAgent(serve.GeminiAgentOptions{
		Client:    client,
		Retriever: retriever,
	})

	ctx := context.Background()
	actions := agent.OnFinalizedLine(ctx, moonshine.Line{ID: 1, Text: "Search for Moonshine", IsComplete: true})

	if len(actions) != 2 {
		t.Fatalf("expected 2 actions (display + speak), got %d: %v", len(actions), actions)
	}

	if actions[0].Verb != "display" {
		t.Errorf("expected first action verb 'display', got %q", actions[0].Verb)
	}

	if actions[1].Verb != "speak" {
		t.Errorf("expected second action verb 'speak', got %q", actions[1].Verb)
	}
}

func TestGeminiAgent_RunCommandGating(t *testing.T) {
	cmdArgs, _ := json.Marshal(map[string]string{"name": "ls"})

	client := &fakeLLMClient{
		turnsFunc: func(history []serve.Turn) (string, []serve.ToolCall, error) {
			lastTurn := history[len(history)-1]
			if lastTurn.Role == "user" {
				return "", []serve.ToolCall{
					{Name: "run_command", Args: cmdArgs},
				}, nil
			}
			return "Done.", nil, nil
		},
	}

	t.Run("run_command disabled by default", func(t *testing.T) {
		agent := serve.NewGeminiAgent(serve.GeminiAgentOptions{
			Client:          client,
			AllowRunCommand: false,
		})

		actions := agent.OnFinalizedLine(context.Background(), moonshine.Line{ID: 1, Text: "run ls", IsComplete: true})
		for _, a := range actions {
			if a.Verb == "run_command" {
				t.Fatalf("expected run_command action to be blocked when AllowRunCommand=false")
			}
		}
	})

	t.Run("run_command enabled", func(t *testing.T) {
		agent := serve.NewGeminiAgent(serve.GeminiAgentOptions{
			Client:          client,
			AllowRunCommand: true,
		})

		actions := agent.OnFinalizedLine(context.Background(), moonshine.Line{ID: 2, Text: "run ls", IsComplete: true})
		found := false
		for _, a := range actions {
			if a.Verb == "run_command" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected run_command action when AllowRunCommand=true")
		}
	})
}
