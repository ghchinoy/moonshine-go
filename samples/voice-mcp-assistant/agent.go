package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
	"google.golang.org/genai"
)

// DefaultGeminiModel is the default model for low-latency voice cascades.
const DefaultGeminiModel = "gemini-3.5-flash-lite"

var (
	pauseRe  = regexp.MustCompile(`(?i)^\s*(stop listening|pause listening)\s*\.?\s*$`)
	resumeRe = regexp.MustCompile(`(?i)^\s*(start listening|resume listening)\s*\.?\s*$`)
)

// ValidateGeminiModel rejects deprecated Gemini 1.x and 2.x models.
func ValidateGeminiModel(model string) error {
	trimmed := strings.TrimSpace(model)
	if strings.HasPrefix(trimmed, "gemini-2.") || strings.HasPrefix(trimmed, "gemini-1.") {
		return fmt.Errorf("deprecated Gemini model %q: use %s, gemini-3.8-flash, or gemini-4-argon", trimmed, DefaultGeminiModel)
	}
	return nil
}

// GeminiMCPClient implements serveapi.LLMClient backed by Google Gemini and
// dynamic tool declarations discovered from an MCPBridge.
//
// Importantly, Gemini 3.5+ and Gemini 4 models attach a cryptographic
// ThoughtSignature to FunctionCall parts and require that exact Content
// (including thought parts and ThoughtSignature) to be replayed when sending
// FunctionResponse turns. GeminiMCPClient caches the raw *genai.Content for
// model tool-call turns so multi-round tool calling succeeds while still
// satisfying the public serveapi.LLMClient interface.
type GeminiMCPClient struct {
	client       *genai.Client
	model        string
	systemPrompt string
	bridge       *MCPBridge

	mu               sync.Mutex
	rawModelContents map[string]*genai.Content
}

// NewGeminiMCPClient constructs a GeminiMCPClient using GEMINI_API_KEY or GOOGLE_API_KEY.
func NewGeminiMCPClient(ctx context.Context, model string, systemPrompt string, bridge *MCPBridge) (*GeminiMCPClient, error) {
	if strings.TrimSpace(model) == "" {
		model = DefaultGeminiModel
	}
	if err := ValidateGeminiModel(model); err != nil {
		return nil, err
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("neither GEMINI_API_KEY nor GOOGLE_API_KEY is set")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("creating genai client: %w", err)
	}

	if systemPrompt == "" {
		systemPrompt = defaultVoiceSystemPrompt
	}

	return &GeminiMCPClient{
		client:           client,
		model:            model,
		systemPrompt:     systemPrompt,
		bridge:           bridge,
		rawModelContents: make(map[string]*genai.Content),
	}, nil
}

const defaultVoiceSystemPrompt = "You are a concise, helpful voice coding assistant connected to a developer workspace via Model Context Protocol (MCP) tools. " +
	"When the user asks about files, directories, git status, or recent commits, call the appropriate MCP tool first, then answer in 1 to 2 short spoken sentences suitable for text-to-speech playback. " +
	"Avoid reading long file paths, commit hashes, or markdown formatting aloud."

// GenerateWithTools satisfies serveapi.LLMClient, converting multi-turn history
// (including previous FunctionCall and FunctionResponse turns) into Gemini Content blocks.
func (c *GeminiMCPClient) GenerateWithTools(ctx context.Context, history []serveapi.Turn) (string, []serveapi.ToolCall, error) {
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
				c.mu.Lock()
				rawContent, ok := c.rawModelContents[key]
				c.mu.Unlock()
				if ok && rawContent != nil {
					contents = append(contents, rawContent)
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
					if err := json.Unmarshal(tr.Result, &respMap); err != nil {
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

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: c.systemPrompt}},
		},
	}

	if c.bridge != nil {
		decls := c.bridge.FunctionDeclarations()
		if len(decls) > 0 {
			cfg.Tools = []*genai.Tool{{FunctionDeclarations: decls}}
		}
	}

	resp, err := c.client.Models.GenerateContent(ctx, c.model, contents, cfg)
	if err != nil {
		return "", nil, err
	}

	var textBuilder strings.Builder
	var calls []serveapi.ToolCall

	for _, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		var candCalls []serveapi.ToolCall
		for _, part := range cand.Content.Parts {
			if part.Text != "" && !part.Thought {
				textBuilder.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				argsBytes, _ := json.Marshal(part.FunctionCall.Args)
				tc := serveapi.ToolCall{
					ID:   part.FunctionCall.ID,
					Name: part.FunctionCall.Name,
					Args: argsBytes,
				}
				calls = append(calls, tc)
				candCalls = append(candCalls, tc)
			}
		}
		if len(candCalls) > 0 {
			c.mu.Lock()
			c.rawModelContents[toolCallsCacheKey(candCalls)] = cand.Content
			c.mu.Unlock()
		}
	}

	return strings.TrimSpace(textBuilder.String()), calls, nil
}

func toolCallsCacheKey(calls []serveapi.ToolCall) string {
	b, _ := json.Marshal(calls)
	return string(b)
}

// TurnMetrics records measured latencies for one voice query -> MCP tool -> answer cycle.
type TurnMetrics struct {
	Prompt         string
	FirstLLMMs     float64
	TotalLLMMs     float64
	TotalMCPToolMs float64
	TotalTurnMs    float64
	ToolCalls      []string
	Answer         string
}

// VoiceMCPAgent implements serveapi.AgentHandler, combining fast-path standby
// pause/resume controls with multi-round Gemini + MCP tool calling.
type VoiceMCPAgent struct {
	llm           serveapi.LLMClient
	bridge        *MCPBridge
	maxToolRounds int
	debug         bool

	mu          sync.Mutex
	history     []serveapi.Turn
	lastMetrics TurnMetrics
}

// NewVoiceMCPAgent constructs a VoiceMCPAgent.
func NewVoiceMCPAgent(llm serveapi.LLMClient, bridge *MCPBridge, maxToolRounds int, debug bool) *VoiceMCPAgent {
	if maxToolRounds <= 0 {
		maxToolRounds = 4
	}
	return &VoiceMCPAgent{
		llm:           llm,
		bridge:        bridge,
		maxToolRounds: maxToolRounds,
		debug:         debug,
	}
}

// ResetHistory clears the multi-turn conversation history (used between isolated benchmark runs).
func (a *VoiceMCPAgent) ResetHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = nil
}

// LastMetrics returns the measured metrics from the most recent OnFinalizedLine turn.
func (a *VoiceMCPAgent) LastMetrics() TurnMetrics {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastMetrics
}

// OnFinalizedLine satisfies serveapi.AgentHandler.
func (a *VoiceMCPAgent) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
	text := strings.TrimSpace(line.Text)
	if text == "" {
		return nil
	}

	// 1. Fast-path standby pause/resume controls (v0.11.0+ native server standby)
	if pauseRe.MatchString(text) {
		fmt.Printf("[%s] [control] session paused in standby (say 'start listening' or press Enter to resume)\n", ts())
		speakArgs, _ := json.Marshal(serveapi.SpeakArgs{Text: "Listening paused."})
		pauseArgs, _ := json.Marshal(serveapi.PauseArgs{
			Passthrough: []string{"start listening", "resume listening"},
		})
		return []serveapi.ActionRequest{
			{Verb: "speak", Args: speakArgs},
			{Verb: "session.pause", Args: pauseArgs},
		}
	}

	if resumeRe.MatchString(text) {
		fmt.Printf("[%s] [control] session resumed (listening enabled)\n", ts())
		speakArgs, _ := json.Marshal(serveapi.SpeakArgs{Text: "Listening resumed."})
		return []serveapi.ActionRequest{
			{Verb: "session.resume"},
			{Verb: "speak", Args: speakArgs},
		}
	}

	if a.llm == nil {
		return nil
	}

	turnStart := time.Now()
	metrics := TurnMetrics{Prompt: text}

	a.mu.Lock()
	a.history = append(a.history, serveapi.Turn{Role: "user", Content: text})
	historyCopy := make([]serveapi.Turn, len(a.history))
	copy(historyCopy, a.history)
	a.mu.Unlock()

	var actions []serveapi.ActionRequest
	var finalAnswer string

	for round := 0; round < a.maxToolRounds; round++ {
		llmStart := time.Now()
		respText, calls, err := a.llm.GenerateWithTools(ctx, historyCopy)
		llmMs := float64(time.Since(llmStart).Microseconds()) / 1000.0
		if round == 0 {
			metrics.FirstLLMMs = llmMs
		}
		metrics.TotalLLMMs += llmMs

		if err != nil {
			fmt.Printf("[%s] [error] Gemini GenerateWithTools failed: %v\n", ts(), err)
			break
		}

		historyCopy = append(historyCopy, serveapi.Turn{
			Role:      "model",
			Content:   respText,
			ToolCalls: calls,
		})

		if len(calls) == 0 {
			finalAnswer = strings.TrimSpace(respText)
			break
		}

		var toolResponses []serveapi.ToolResponse
		for _, call := range calls {
			metrics.ToolCalls = append(metrics.ToolCalls, call.Name)
			fmt.Printf("[%s] [mcp-call] %s(%s)\n", ts(), call.Name, string(call.Args))

			var resBytes json.RawMessage
			var callDur time.Duration
			var callErr error
			if a.bridge != nil {
				resBytes, callDur, callErr = a.bridge.CallTool(ctx, call.Name, call.Args)
			} else {
				callErr = fmt.Errorf("no MCP bridge configured")
			}

			toolMs := float64(callDur.Microseconds()) / 1000.0
			metrics.TotalMCPToolMs += toolMs

			if callErr != nil {
				fmt.Printf("[%s] [mcp-err]  %s failed (%.1fms): %v\n", ts(), call.Name, toolMs, callErr)
				errJSON, _ := json.Marshal(map[string]string{"error": callErr.Error()})
				resBytes = errJSON
			} else {
				fmt.Printf("[%s] [mcp-ok]   %s completed in %.1fms\n", ts(), call.Name, toolMs)
				if a.debug {
					fmt.Printf("[%s] [debug]    result: %s\n", ts(), string(resBytes))
				}
			}

			cardArgs, _ := json.Marshal(serveapi.DisplayCard{
				Title: fmt.Sprintf("MCP Tool: %s (%.1fms)", call.Name, toolMs),
				Body:  string(resBytes),
				Kind:  "tool",
			})
			actions = append(actions, serveapi.ActionRequest{
				Verb: "display",
				Args: cardArgs,
			})

			toolResponses = append(toolResponses, serveapi.ToolResponse{
				ID:     call.ID,
				Name:   call.Name,
				Result: resBytes,
			})
		}

		historyCopy = append(historyCopy, serveapi.Turn{
			Role:          "tool",
			ToolResponses: toolResponses,
		})
	}

	metrics.TotalTurnMs = float64(time.Since(turnStart).Microseconds()) / 1000.0
	metrics.Answer = finalAnswer

	a.mu.Lock()
	a.history = historyCopy
	a.lastMetrics = metrics
	a.mu.Unlock()

	if finalAnswer != "" {
		fmt.Printf("[%s] [assistant] %s  (first_llm: %.0fms, total_llm: %.0fms, mcp: %.1fms, total: %.0fms)\n",
			ts(), finalAnswer, metrics.FirstLLMMs, metrics.TotalLLMMs, metrics.TotalMCPToolMs, metrics.TotalTurnMs)

		cardArgs, _ := json.Marshal(serveapi.DisplayCard{
			Title: "Assistant Response",
			Body:  finalAnswer,
			Kind:  "answer",
		})
		speakArgs, _ := json.Marshal(serveapi.SpeakArgs{Text: finalAnswer})
		actions = append(actions,
			serveapi.ActionRequest{Verb: "display", Args: cardArgs},
			serveapi.ActionRequest{Verb: "speak", Args: speakArgs},
		)
	}

	return actions
}
