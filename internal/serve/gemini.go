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

// DefaultGeminiModel is the default Google Gemini model used for voice cascades.
const DefaultGeminiModel = "gemini-3.5-flash-lite"

const defaultVoiceSystemPrompt = "You are an agentic voice assistant. Use available tools (lookup, display_card, speak) to fulfill user requests concisely."

// RealGeminiClient wraps google.golang.org/genai.
type RealGeminiClient struct {
	client       *genai.Client
	model        string
	systemPrompt string

	mu               sync.Mutex
	rawModelContents map[string]*genai.Content
}

// NewRealGeminiClient creates a RealGeminiClient using GEMINI_API_KEY / GOOGLE_API_KEY environment variables.
func NewRealGeminiClient(ctx context.Context, model string) (*RealGeminiClient, error) {
	if model == "" {
		model = DefaultGeminiModel
	}
	if strings.HasPrefix(model, "gemini-2.") || strings.HasPrefix(model, "gemini-1.") {
		return nil, fmt.Errorf("deprecated Gemini model %q: use %s, gemini-3.8-flash, or gemini-4-argon", model, DefaultGeminiModel)
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("neither GEMINI_API_KEY nor GOOGLE_API_KEY is set")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("creating genai client: %w", err)
	}

	return &RealGeminiClient{
		client:           client,
		model:            model,
		systemPrompt:     defaultVoiceSystemPrompt,
		rawModelContents: make(map[string]*genai.Content),
	}, nil
}

// SetSystemPrompt configures the system instruction for Gemini.
func (c *RealGeminiClient) SetSystemPrompt(prompt string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systemPrompt = prompt
}

func (c *RealGeminiClient) cacheRawContent(key string, content *genai.Content) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.rawModelContents) > 100 {
		count := 0
		for k := range c.rawModelContents {
			delete(c.rawModelContents, k)
			count++
			if count > 50 {
				break
			}
		}
	}
	c.rawModelContents[key] = content
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

	c.mu.Lock()
	cachedCopy := make(map[string]*genai.Content, len(c.rawModelContents))
	for k, v := range c.rawModelContents {
		cachedCopy[k] = v
	}
	prompt := c.systemPrompt
	c.mu.Unlock()

	contents := buildGenaiContents(history, cachedCopy)

	config := &genai.GenerateContentConfig{
		Tools: tools,
	}
	if prompt != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: prompt}},
		}
	}

	resp, err := c.client.Models.GenerateContent(ctx, c.model, contents, config)
	if err != nil {
		return "", nil, err
	}

	text, calls, candContent := extractCandidateOutput(resp.Candidates)
	if len(calls) > 0 && candContent != nil {
		c.cacheRawContent(toolCallsCacheKey(calls), candContent)
	}

	return text, calls, nil
}

func toolCallsCacheKey(calls []ToolCall) string {
	b, _ := json.Marshal(calls)
	return string(b)
}

func buildGenaiContents(history []Turn, rawContents map[string]*genai.Content) []*genai.Content {
	var contents []*genai.Content
	for _, turn := range history {
		switch turn.Role {
		case "user":
			if turn.Content != "" {
				contents = append(contents, &genai.Content{
					Role:  "user",
					Parts: []*genai.Part{{Text: turn.Content}},
				})
			}
		case "model":
			if len(turn.ToolCalls) > 0 {
				key := toolCallsCacheKey(turn.ToolCalls)
				if raw, ok := rawContents[key]; ok && raw != nil {
					contents = append(contents, raw)
					continue
				}
			}
			var parts []*genai.Part
			if turn.Content != "" {
				parts = append(parts, &genai.Part{Text: turn.Content})
			}
			for _, tc := range turn.ToolCalls {
				var argsMap map[string]any
				if len(tc.Args) > 0 {
					_ = json.Unmarshal(tc.Args, &argsMap)
				}
				if argsMap == nil {
					argsMap = map[string]any{}
				}
				parts = append(parts, genai.NewPartFromFunctionCall(tc.Name, argsMap))
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  "model",
					Parts: parts,
				})
			}
		case "tool":
			var parts []*genai.Part
			for _, tr := range turn.ToolResponses {
				var respMap map[string]any
				if len(tr.Result) > 0 {
					var raw any
					if err := json.Unmarshal(tr.Result, &raw); err == nil {
						if m, ok := raw.(map[string]any); ok {
							respMap = m
						} else {
							respMap = map[string]any{"result": raw}
						}
					} else {
						respMap = map[string]any{"output": string(tr.Result)}
					}
				}
				if respMap == nil {
					respMap = map[string]any{}
				}
				parts = append(parts, genai.NewPartFromFunctionResponse(tr.Name, respMap))
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  "user",
					Parts: parts,
				})
			}
		}
	}
	return contents
}

func extractCandidateOutput(candidates []*genai.Candidate) (string, []ToolCall, *genai.Content) {
	var textBuilder strings.Builder
	var calls []ToolCall
	var candWithCalls *genai.Content

	for _, cand := range candidates {
		if cand.Content == nil {
			continue
		}
		var candCalls []ToolCall
		for _, part := range cand.Content.Parts {
			if part.Text != "" && !part.Thought {
				textBuilder.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				argsBytes, _ := json.Marshal(part.FunctionCall.Args)
				tc := ToolCall{
					ID:   part.FunctionCall.ID,
					Name: part.FunctionCall.Name,
					Args: argsBytes,
				}
				calls = append(calls, tc)
				candCalls = append(candCalls, tc)
			}
		}
		if len(candCalls) > 0 && candWithCalls == nil {
			candWithCalls = cand.Content
		}
	}

	return strings.TrimSpace(textBuilder.String()), calls, candWithCalls
}
