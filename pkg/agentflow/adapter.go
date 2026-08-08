package agentflow

import (
	"context"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// HandlerAdapter adapts an AgentFlow instance to satisfy serveapi.AgentHandler.
// It forwards finalized lines into AgentFlow.HandleUtterance.
type HandlerAdapter struct {
	flow *AgentFlow
}

// NewHandlerAdapter wraps flow in a serveapi.AgentHandler adapter.
func NewHandlerAdapter(flow *AgentFlow) serveapi.AgentHandler {
	return &HandlerAdapter{flow: flow}
}

// OnFinalizedLine satisfies serveapi.AgentHandler by forwarding line.Text to HandleUtterance.
func (a *HandlerAdapter) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
	if a.flow != nil && line.Text != "" {
		a.flow.HandleUtterance(line.Text)
	}
	return nil
}
