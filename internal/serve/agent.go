package serve

import (
	"context"
	"sync"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

// AgentHandler is the interface for agent logic that reacts to finalized utterances.
type AgentHandler interface {
	OnFinalizedLine(ctx context.Context, line moonshine.Line) []event.ActionRequest
}

// ExternalAgent is an AgentHandler that performs no in-process handling.
// It is the default mode when external IPC subscribers own all agent logic.
type ExternalAgent struct{}

// OnFinalizedLine satisfies AgentHandler, returning nil.
func (e ExternalAgent) OnFinalizedLine(ctx context.Context, line moonshine.Line) []event.ActionRequest {
	return nil
}

// CompositeHandler combines multiple AgentHandlers in order.
// For each finalized line, it evaluates handlers in sequence and returns
// the actions from the first handler that returns a non-empty slice.
type CompositeHandler struct {
	handlers []AgentHandler
}

// NewCompositeHandler creates a CompositeHandler from the provided handlers.
func NewCompositeHandler(handlers ...AgentHandler) *CompositeHandler {
	return &CompositeHandler{handlers: handlers}
}

// OnFinalizedLine evaluates handlers in order and returns the first non-empty action list.
func (c *CompositeHandler) OnFinalizedLine(ctx context.Context, line moonshine.Line) []event.ActionRequest {
	for _, h := range c.handlers {
		if h == nil {
			continue
		}
		if actions := h.OnFinalizedLine(ctx, line); len(actions) > 0 {
			return actions
		}
	}
	return nil
}

// ActionSink defines the interface for dispatching ActionRequests emitted by an agent.
type ActionSink interface {
	Dispatch(ctx context.Context, req event.ActionRequest) (event.ActionResult, error)
}

// ActionSinkFunc allows a plain function to satisfy the ActionSink interface.
type ActionSinkFunc func(ctx context.Context, req event.ActionRequest) (event.ActionResult, error)

// Dispatch satisfies ActionSink by invoking the underlying function.
func (f ActionSinkFunc) Dispatch(ctx context.Context, req event.ActionRequest) (event.ActionResult, error) {
	return f(ctx, req)
}

// AgentRunner processes TranscriptEvents, deduplicates finalized lines by ID,
// passes each new finalized line to the configured AgentHandler, and forwards
// any resulting ActionRequests to the ActionSink.
type AgentRunner struct {
	handler AgentHandler
	sink    ActionSink

	mu   sync.Mutex
	seen map[uint64]bool
}

// NewAgentRunner creates a new AgentRunner with the given handler and sink.
func NewAgentRunner(handler AgentHandler, sink ActionSink) *AgentRunner {
	if handler == nil {
		handler = ExternalAgent{}
	}
	return &AgentRunner{
		handler: handler,
		sink:    sink,
		seen:    make(map[uint64]bool),
	}
}

// ProcessEvent extracts newly finalized lines from ev, deduplicates them by Line.ID,
// and passes each to the handler.
func (r *AgentRunner) ProcessEvent(ctx context.Context, ev event.TranscriptEvent) {
	finalized := ev.FinalizedLines()
	for _, line := range finalized {
		r.ProcessLine(ctx, line)
	}
}

// ProcessLine processes a single finalized line if it hasn't been seen yet.
func (r *AgentRunner) ProcessLine(ctx context.Context, line moonshine.Line) {
	if !line.IsComplete {
		return
	}

	r.mu.Lock()
	if r.seen[line.ID] {
		r.mu.Unlock()
		return
	}
	r.seen[line.ID] = true
	r.mu.Unlock()

	actions := r.handler.OnFinalizedLine(ctx, line)
	if len(actions) == 0 || r.sink == nil {
		return
	}

	for _, req := range actions {
		_, _ = r.sink.Dispatch(ctx, req)
	}
}

// Run pumps events from the events channel until ctx is canceled or the channel is closed.
func (r *AgentRunner) Run(ctx context.Context, events <-chan event.TranscriptEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			r.ProcessEvent(ctx, ev)
		}
	}
}
