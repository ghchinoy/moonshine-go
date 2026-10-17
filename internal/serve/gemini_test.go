package serve_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ghchinoy/moonshine-go/internal/serve"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
	"google.golang.org/genai"
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
	actions := agent.OnFinalizedLine(ctx, serveapi.Line{ID: 1, Text: "Search for Moonshine", IsComplete: true})

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

		actions := agent.OnFinalizedLine(context.Background(), serveapi.Line{ID: 1, Text: "run ls", IsComplete: true})
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

		actions := agent.OnFinalizedLine(context.Background(), serveapi.Line{ID: 2, Text: "run ls", IsComplete: true})
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

func TestNewRealGeminiClient_ModelValidation(t *testing.T) {
	if serve.DefaultGeminiModel != "gemini-3.5-flash-lite" {
		t.Errorf("DefaultGeminiModel = %q, want 'gemini-3.5-flash-lite'", serve.DefaultGeminiModel)
	}

	ctx := context.Background()

	// 1. Deprecated models must be rejected immediately
	deprecatedModels := []string{
		"gemini-2.5-flash",
		"gemini-2.0-flash",
		"gemini-2.0-pro-exp",
		"gemini-1.5-flash",
		"gemini-1.5-pro",
	}

	for _, m := range deprecatedModels {
		t.Run("reject_"+m, func(t *testing.T) {
			_, err := serve.NewRealGeminiClient(ctx, m)
			if err == nil {
				t.Fatalf("expected error rejecting %q, got nil", m)
			}
			wantErrSubstring := "deprecated Gemini model"
			if !strings.Contains(err.Error(), wantErrSubstring) {
				t.Errorf("error %q does not contain %q", err.Error(), wantErrSubstring)
			}
		})
	}
}

func TestExtractCandidateOutput_ThoughtFilteringAndThoughtSignature(t *testing.T) {
	cand := &genai.Candidate{
		Content: &genai.Content{
			Role: "model",
			Parts: []*genai.Part{
				{
					Text:    "Thinking: user wants to know about moonshine...",
					Thought: true,
				},
				{
					FunctionCall: &genai.FunctionCall{
						ID:   "call-lookup-1",
						Name: "lookup",
						Args: map[string]any{"query": "moonshine"},
					},
					ThoughtSignature: []byte("test-cryptographic-thought-signature-12345"),
				},
				{
					Text:    "Checking the documentation now.",
					Thought: false,
				},
			},
		},
	}

	text, calls, candContent := serve.ExtractCandidateOutput([]*genai.Candidate{cand})

	// 1. Spoken text must NOT contain thinking monologue
	if strings.Contains(text, "Thinking:") {
		t.Errorf("expected thought text to be filtered out, got: %q", text)
	}
	if text != "Checking the documentation now." {
		t.Errorf("expected text %q, got %q", "Checking the documentation now.", text)
	}

	// 2. ToolCalls must be extracted with ID, Name, and Args
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].ID != "call-lookup-1" {
		t.Errorf("call ID = %q, want 'call-lookup-1'", calls[0].ID)
	}
	if calls[0].Name != "lookup" {
		t.Errorf("call Name = %q, want 'lookup'", calls[0].Name)
	}

	// 3. Raw content must be captured and preserve ThoughtSignature
	if candContent == nil {
		t.Fatal("expected candContent to be non-nil")
	}
	if len(candContent.Parts) != 3 {
		t.Fatalf("expected 3 parts in raw candContent, got %d", len(candContent.Parts))
	}
	if string(candContent.Parts[1].ThoughtSignature) != "test-cryptographic-thought-signature-12345" {
		t.Errorf("ThoughtSignature not preserved in raw content: %q", string(candContent.Parts[1].ThoughtSignature))
	}
}

func TestBuildGenaiContents_ThoughtSignatureReplayAndToolResponses(t *testing.T) {
	// Replayable raw model content with ThoughtSignature
	rawModelContent := &genai.Content{
		Role: "model",
		Parts: []*genai.Part{
			{
				Text:    "Internal reasoning...",
				Thought: true,
			},
			{
				FunctionCall: &genai.FunctionCall{
					ID:   "call-lookup-1",
					Name: "lookup",
					Args: map[string]any{"query": "moonshine"},
				},
				ThoughtSignature: []byte("test-cryptographic-thought-signature-12345"),
			},
		},
	}

	callArgs, _ := json.Marshal(map[string]any{"query": "moonshine"})
	toolCalls := []serve.ToolCall{
		{
			ID:   "call-lookup-1",
			Name: "lookup",
			Args: callArgs,
		},
	}

	cacheKey := serve.ToolCallsCacheKey(toolCalls)
	rawContents := map[string]*genai.Content{
		cacheKey: rawModelContent,
	}

	// Simulate turn 1: user query, turn 2: model tool call, turn 3: tool result (array result from lookup)
	lookupResultJSON := []byte(`[{"title":"Moonshine Docs","snippet":"ASR model"}]`)
	history := []serve.Turn{
		{Role: "user", Content: "Tell me about moonshine"},
		{Role: "model", ToolCalls: toolCalls},
		{Role: "tool", ToolResponses: []serve.ToolResponse{
			{ID: "call-lookup-1", Name: "lookup", Result: lookupResultJSON},
		}},
	}

	contents := serve.BuildGenaiContents(history, rawContents)

	if len(contents) != 3 {
		t.Fatalf("expected 3 Content objects, got %d", len(contents))
	}

	// Content 0: User turn
	if contents[0].Role != "user" || contents[0].Parts[0].Text != "Tell me about moonshine" {
		t.Errorf("unexpected content 0: %+v", contents[0])
	}

	// Content 1: Model turn with ThoughtSignature REPLAYED
	if contents[1] != rawModelContent {
		t.Errorf("expected cached rawModelContent with ThoughtSignature to be replayed directly")
	}
	if string(contents[1].Parts[1].ThoughtSignature) != "test-cryptographic-thought-signature-12345" {
		t.Errorf("ThoughtSignature missing from replayed model content")
	}

	// Content 2: Tool response serialized as user turn with FunctionResponse
	if contents[2].Role != "user" {
		t.Errorf("expected tool response Content role 'user', got %q", contents[2].Role)
	}
	if len(contents[2].Parts) != 1 || contents[2].Parts[0].FunctionResponse == nil {
		t.Fatalf("expected FunctionResponse part, got: %+v", contents[2].Parts)
	}
	fr := contents[2].Parts[0].FunctionResponse
	if fr.Name != "lookup" {
		t.Errorf("FunctionResponse name = %q, want 'lookup'", fr.Name)
	}
	if fr.Response == nil {
		t.Errorf("FunctionResponse response map is nil")
	}

	// Fallback test: model turn without cache should serialize FunctionCall
	contentsFallback := serve.BuildGenaiContents(history, nil)
	if len(contentsFallback) != 3 {
		t.Fatalf("expected 3 Content objects in fallback, got %d", len(contentsFallback))
	}
	if contentsFallback[1].Role != "model" || contentsFallback[1].Parts[0].FunctionCall == nil {
		t.Fatalf("expected fallback model content with FunctionCall part, got: %+v", contentsFallback[1])
	}
	if contentsFallback[1].Parts[0].FunctionCall.Name != "lookup" {
		t.Errorf("fallback FunctionCall name = %q, want 'lookup'", contentsFallback[1].Parts[0].FunctionCall.Name)
	}
}
