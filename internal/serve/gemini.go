package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
	"google.golang.org/genai"
)

// ToolCall, ToolResponse, Turn, and LLMClient are aliases of the
// identically-named pkg/serveapi types: the public package is the source of
// truth (and what external Tier-2 Go consumers implement LLMClient against),
// this package just wires the built-in Gemini implementation to it.
type (
	ToolCall     = serveapi.ToolCall
	ToolResponse = serveapi.ToolResponse
	Turn         = serveapi.Turn
	LLMClient    = serveapi.LLMClient
)

// GeminiAgentOptions holds configuration options for GeminiAgent.
type GeminiAgentOptions struct {
	Model           string
	SystemPrompt    string
	AllowRunCommand bool
	Retriever       Retriever
	Client          LLMClient
}

// GeminiAgent implements AgentHandler using Gemini function-calling.
type GeminiAgent struct {
	client          LLMClient
	retriever       Retriever
	allowRunCommand bool
	systemPrompt    string

	mu      sync.Mutex
	history []Turn
}

// NewGeminiAgent constructs a GeminiAgent. If opts.Retriever is nil, NoopRetriever is used.
func NewGeminiAgent(opts GeminiAgentOptions) *GeminiAgent {
	if opts.Retriever == nil {
		opts.Retriever = NoopRetriever{}
	}
	if opts.SystemPrompt == "" {
		opts.SystemPrompt = "You are an agentic voice assistant. Use available tools (lookup, display_card, speak) to fulfill user requests concisely."
	}
	return &GeminiAgent{
		client:          opts.Client,
		retriever:       opts.Retriever,
		allowRunCommand: opts.AllowRunCommand,
		systemPrompt:    opts.SystemPrompt,
	}
}

// OnFinalizedLine satisfies AgentHandler.
func (g *GeminiAgent) OnFinalizedLine(ctx context.Context, line serveapi.Line) []event.ActionRequest {
	text := strings.TrimSpace(line.Text)
	if text == "" || g.client == nil {
		return nil
	}

	g.mu.Lock()
	g.history = append(g.history, Turn{Role: "user", Content: text})
	historyCopy := make([]Turn, len(g.history))
	copy(historyCopy, g.history)
	g.mu.Unlock()

	var actions []event.ActionRequest
	spokenText := false
	maxTurns := 5

	for turnCount := 0; turnCount < maxTurns; turnCount++ {
		respText, calls, err := g.client.GenerateWithTools(ctx, historyCopy)
		if err != nil {
			break
		}

		modelTurn := Turn{Role: "model", Content: respText, ToolCalls: calls}
		historyCopy = append(historyCopy, modelTurn)

		if len(calls) == 0 {
			if respText != "" && !spokenText {
				speakArgs, _ := json.Marshal(event.SpeakArgs{Text: respText})
				actions = append(actions, event.ActionRequest{
					Verb: "speak",
					Args: speakArgs,
				})
			}
			break
		}

		var toolResponses []ToolResponse
		for _, call := range calls {
			switch call.Name {
			case "lookup":
				var args struct {
					Query string `json:"query"`
				}
				_ = json.Unmarshal(call.Args, &args)
				results, _ := g.retriever.Retrieve(ctx, args.Query)
				resJSON, _ := json.Marshal(results)
				toolResponses = append(toolResponses, ToolResponse{
					ID:     call.ID,
					Name:   call.Name,
					Result: resJSON,
				})

			case "display_card":
				var card event.DisplayCard
				_ = json.Unmarshal(call.Args, &card)
				cardArgs, _ := json.Marshal(card)
				actions = append(actions, event.ActionRequest{
					Verb: "display",
					Args: cardArgs,
				})
				resJSON, _ := json.Marshal(map[string]bool{"displayed": true})
				toolResponses = append(toolResponses, ToolResponse{
					ID:     call.ID,
					Name:   call.Name,
					Result: resJSON,
				})

			case "speak":
				var speakArgs event.SpeakArgs
				_ = json.Unmarshal(call.Args, &speakArgs)
				argsJSON, _ := json.Marshal(speakArgs)
				actions = append(actions, event.ActionRequest{
					Verb: "speak",
					Args: argsJSON,
				})
				spokenText = true
				resJSON, _ := json.Marshal(map[string]bool{"spoken": true})
				toolResponses = append(toolResponses, ToolResponse{
					ID:     call.ID,
					Name:   call.Name,
					Result: resJSON,
				})

			case "run_command":
				if !g.allowRunCommand {
					resJSON, _ := json.Marshal(map[string]string{"error": "run_command action is disabled"})
					toolResponses = append(toolResponses, ToolResponse{
						ID:     call.ID,
						Name:   call.Name,
						Result: resJSON,
					})
				} else {
					actions = append(actions, event.ActionRequest{
						Verb: "run_command",
						Args: call.Args,
					})
					resJSON, _ := json.Marshal(map[string]bool{"queued": true})
					toolResponses = append(toolResponses, ToolResponse{
						ID:     call.ID,
						Name:   call.Name,
						Result: resJSON,
					})
				}
			}
		}

		historyCopy = append(historyCopy, Turn{Role: "tool", ToolResponses: toolResponses})
	}

	g.mu.Lock()
	g.history = historyCopy
	g.mu.Unlock()

	return actions
}

// RealGeminiClient wraps google.golang.org/genai.
type RealGeminiClient struct {
	client *genai.Client
	model  string
}

// NewRealGeminiClient creates a RealGeminiClient using GEMINI_API_KEY / GOOGLE_API_KEY environment variables.
func NewRealGeminiClient(ctx context.Context, model string) (*RealGeminiClient, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("neither GEMINI_API_KEY nor GOOGLE_API_KEY is set")
	}
	if model == "" {
		model = "gemini-2.5-flash"
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("creating genai client: %w", err)
	}

	return &RealGeminiClient{
		client: client,
		model:  model,
	}, nil
}

// GenerateWithTools implements LLMClient using Google Gemini.
func (c *RealGeminiClient) GenerateWithTools(ctx context.Context, history []Turn) (string, []ToolCall, error) {
	tools := []*genai.Tool{
		{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				{
					Name:        "lookup",
					Description: "Search or retrieve relevant information/documentation.",
					Parameters: &genai.Schema{
						Type: genai.TypeObject,
						Properties: map[string]*genai.Schema{
							"query": {Type: genai.TypeString, Description: "Search query"},
						},
						Required: []string{"query"},
					},
				},
				{
					Name:        "display_card",
					Description: "Display a card UI element to connected subscribers.",
					Parameters: &genai.Schema{
						Type: genai.TypeObject,
						Properties: map[string]*genai.Schema{
							"title": {Type: genai.TypeString, Description: "Card title"},
							"body":  {Type: genai.TypeString, Description: "Card text body"},
							"kind":  {Type: genai.TypeString, Description: "Card type (info/lookup/status)"},
						},
						Required: []string{"title"},
					},
				},
				{
					Name:        "speak",
					Description: "Synthesize speech and play it back via TTS.",
					Parameters: &genai.Schema{
						Type: genai.TypeObject,
						Properties: map[string]*genai.Schema{
							"text": {Type: genai.TypeString, Description: "Text to speak"},
						},
						Required: []string{"text"},
					},
				},
				{
					Name:        "run_command",
					Description: "Execute a system command (gated).",
					Parameters: &genai.Schema{
						Type: genai.TypeObject,
						Properties: map[string]*genai.Schema{
							"name": {Type: genai.TypeString, Description: "Command name"},
						},
						Required: []string{"name"},
					},
				},
			},
		},
	}

	var contents []*genai.Content
	for _, turn := range history {
		if turn.Content != "" {
			contents = append(contents, &genai.Content{
				Role: turn.Role,
				Parts: []*genai.Part{
					{Text: turn.Content},
				},
			})
		}
	}

	config := &genai.GenerateContentConfig{
		Tools: tools,
	}

	resp, err := c.client.Models.GenerateContent(ctx, c.model, contents, config)
	if err != nil {
		return "", nil, err
	}

	var text string
	var calls []ToolCall

	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					text += part.Text
				}
				if part.FunctionCall != nil {
					argsBytes, _ := json.Marshal(part.FunctionCall.Args)
					calls = append(calls, ToolCall{
						Name: part.FunctionCall.Name,
						Args: argsBytes,
					})
				}
			}
		}
	}

	return text, calls, nil
}
