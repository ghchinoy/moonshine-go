package serveapi

import (
	"context"
	"sync"
)

// AgentHandler is the interface for agent logic that reacts to finalized
// utterances. It is called once per newly-finalized line and returns any
// actions to dispatch (which may be empty).
type AgentHandler interface {
	OnFinalizedLine(ctx context.Context, line Line) []ActionRequest
}

// ExternalAgent is an AgentHandler that performs no in-process handling. It is
// the default when external IPC subscribers own all agent logic.
type ExternalAgent struct{}

// OnFinalizedLine satisfies AgentHandler, returning nil.
func (ExternalAgent) OnFinalizedLine(ctx context.Context, line Line) []ActionRequest {
	return nil
}

// CompositeHandler combines multiple AgentHandlers in order. For each
// finalized line it evaluates handlers in sequence and returns the actions
// from the first handler that returns a non-empty slice -- e.g. a fast-path
// intent matcher first, falling through to an LLM agent on no match.
type CompositeHandler struct {
	handlers []AgentHandler
}

// NewCompositeHandler creates a CompositeHandler from the provided handlers.
func NewCompositeHandler(handlers ...AgentHandler) *CompositeHandler {
	return &CompositeHandler{handlers: handlers}
}

// OnFinalizedLine evaluates handlers in order, returning the first non-empty
// action list. Nil handlers are skipped.
func (c *CompositeHandler) OnFinalizedLine(ctx context.Context, line Line) []ActionRequest {
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

// ActionSink dispatches ActionRequests emitted by an agent.
type ActionSink interface {
	Dispatch(ctx context.Context, req ActionRequest) (ActionResult, error)
}

// ActionSinkFunc lets a plain function satisfy ActionSink.
type ActionSinkFunc func(ctx context.Context, req ActionRequest) (ActionResult, error)

// Dispatch satisfies ActionSink by invoking the underlying function.
func (f ActionSinkFunc) Dispatch(ctx context.Context, req ActionRequest) (ActionResult, error) {
	return f(ctx, req)
}

// AgentRunner processes TranscriptEvents, deduplicates finalized lines by ID,
// passes each newly-finalized line to the configured AgentHandler, and forwards
// any resulting ActionRequests to the ActionSink asynchronously in separate goroutines.
//
// Because ActionSink.Dispatch calls run asynchronously, slow or blocking
// action dispatches (e.g. a long TTS speak action waiting for playback completion)
// do not block AgentRunner.Run's event-reading loop or backpressure the caller's
// event-feeding pipeline.
//
// It is idempotent on Line.ID: even if the same finalized line appears in
// multiple events (the sidecar's updates are supersets, not exact frames),
// each line is handled exactly once.
type AgentRunner struct {
	handler AgentHandler
	sink    ActionSink

	mu   sync.Mutex
	seen map[uint64]bool
}

// NewAgentRunner creates an AgentRunner. A nil handler defaults to
// ExternalAgent (no in-process handling).
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

// ProcessEvent extracts newly-finalized lines from ev and processes each.
func (r *AgentRunner) ProcessEvent(ctx context.Context, ev TranscriptEvent) {
	for _, line := range ev.FinalizedLines() {
		r.ProcessLine(ctx, line)
	}
}

// ProcessLine processes a single finalized line if it hasn't been seen before.
// Non-finalized lines are ignored.
func (r *AgentRunner) ProcessLine(ctx context.Context, line Line) {
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
		req := req
		go func() {
			_, _ = r.sink.Dispatch(ctx, req)
		}()
	}
}

// Run pumps events from the channel until ctx is canceled or the channel is
// closed. Blocks; call it from a goroutine.
func (r *AgentRunner) Run(ctx context.Context, events <-chan TranscriptEvent) {
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
