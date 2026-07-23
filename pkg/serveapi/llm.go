package serveapi

import (
	"context"
	"encoding/json"
)

// ToolCall represents a tool invocation requested by an LLM.
type ToolCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ToolResponse represents the execution result of a tool call, returned to the
// LLM on a subsequent turn.
type ToolResponse struct {
	ID     string          `json:"id,omitempty"`
	Name   string          `json:"name"`
	Result json.RawMessage `json:"result"`
}

// Turn represents a single message turn in a multi-turn agent conversation.
type Turn struct {
	Role          string         `json:"role"` // "user", "model", or "tool"
	Content       string         `json:"content,omitempty"`
	ToolCalls     []ToolCall     `json:"tool_calls,omitempty"`
	ToolResponses []ToolResponse `json:"tool_responses,omitempty"`
}

// LLMClient abstracts communication with an LLM. It is deliberately decoupled
// from any specific SDK: the built-in Gemini implementation lives in
// internal/serve (it imports the Gemini SDK), while tests and alternative
// backends can supply their own implementation of this interface.
type LLMClient interface {
	GenerateWithTools(ctx context.Context, history []Turn) (text string, calls []ToolCall, err error)
}
