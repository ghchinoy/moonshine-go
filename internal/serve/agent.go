package serve

import (
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// AgentHandler, ExternalAgent, CompositeHandler, ActionSink, ActionSinkFunc,
// and AgentRunner are aliases of the identically-named pkg/serveapi types:
// the public package is the source of truth (and is what external Tier-2 Go
// consumers implement against), this package just wires the built-in
// implementations (GeminiAgent, IntentMatcher) to it.
type (
	AgentHandler     = serveapi.AgentHandler
	ExternalAgent    = serveapi.ExternalAgent
	CompositeHandler = serveapi.CompositeHandler
	ActionSink       = serveapi.ActionSink
	ActionSinkFunc   = serveapi.ActionSinkFunc
	AgentRunner      = serveapi.AgentRunner
)

// NewCompositeHandler creates a CompositeHandler from the provided handlers.
func NewCompositeHandler(handlers ...AgentHandler) *CompositeHandler {
	return serveapi.NewCompositeHandler(handlers...)
}

// NewAgentRunner creates a new AgentRunner with the given handler and sink.
func NewAgentRunner(handler AgentHandler, sink ActionSink) *AgentRunner {
	return serveapi.NewAgentRunner(handler, sink)
}
