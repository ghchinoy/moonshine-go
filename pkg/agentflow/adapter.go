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
func NewHandlerAdapter(flow *AgentFlow) *HandlerAdapter {
	return &HandlerAdapter{flow: flow}
}

// SetActionSink binds an ActionSink to the underlying AgentFlow.
func (a *HandlerAdapter) SetActionSink(sink serveapi.ActionSink) {
	if a.flow != nil {
		a.flow.ActionSink(sink)
	}
}

// OnFinalizedLine satisfies serveapi.AgentHandler by forwarding line.Text to HandleUtterance.
func (a *HandlerAdapter) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
	if a.flow != nil && line.Text != "" {
		a.flow.HandleUtterance(line.Text)
	}
	return nil
}
