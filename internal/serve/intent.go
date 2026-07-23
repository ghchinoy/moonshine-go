package serve

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

// IntentRule defines a single pattern-matching rule for fast-path voice commands.
type IntentRule struct {
	Pattern       *regexp.Regexp
	ActionBuilder func(matches []string) []event.ActionRequest
}

// IntentMatcher implements a deterministic, offline regex/rule-based intent matcher.
// It implements AgentHandler so it can run standalone or as a stage in a CompositeHandler.
type IntentMatcher struct {
	rules []IntentRule
}

// NewIntentMatcher creates an IntentMatcher with custom or default rules.
func NewIntentMatcher(rules ...IntentRule) *IntentMatcher {
	if len(rules) == 0 {
		rules = DefaultIntentRules()
	}
	return &IntentMatcher{rules: rules}
}

// DefaultIntentRules returns the standard set of fast-path voice command rules:
//   - "stop listening" / "pause listening" -> session.pause
//   - "resume listening" / "start listening" -> session.resume
//   - "stop session" / "stop sidecar" -> session.stop
//   - "say <text>" / "repeat <text>" -> speak{text: "<text>"}
func DefaultIntentRules() []IntentRule {
	return []IntentRule{
		{
			Pattern: regexp.MustCompile(`(?i)^\s*(stop|pause)\s+listening\s*\.?$`),
			ActionBuilder: func(matches []string) []event.ActionRequest {
				return []event.ActionRequest{{Verb: "session.pause"}}
			},
		},
		{
			Pattern: regexp.MustCompile(`(?i)^\s*(resume|start)\s+listening\s*\.?$`),
			ActionBuilder: func(matches []string) []event.ActionRequest {
				return []event.ActionRequest{{Verb: "session.resume"}}
			},
		},
		{
			Pattern: regexp.MustCompile(`(?i)^\s*stop\s+(session|sidecar)\s*\.?$`),
			ActionBuilder: func(matches []string) []event.ActionRequest {
				return []event.ActionRequest{{Verb: "session.stop"}}
			},
		},
		{
			Pattern: regexp.MustCompile(`(?i)^\s*(say|repeat)\s+(.+?)\s*\.?$`),
			ActionBuilder: func(matches []string) []event.ActionRequest {
				if len(matches) < 3 {
					return nil
				}
				text := strings.TrimSpace(matches[2])
				if text == "" {
					return nil
				}
				args, _ := json.Marshal(event.SpeakArgs{Text: text})
				return []event.ActionRequest{
					{
						Verb: "speak",
						Args: args,
					},
				}
			},
		},
	}
}

// OnFinalizedLine satisfies AgentHandler. On a match, returns ActionRequests; returns nil on miss.
func (m *IntentMatcher) OnFinalizedLine(ctx context.Context, line moonshine.Line) []event.ActionRequest {
	text := strings.TrimSpace(line.Text)
	if text == "" {
		return nil
	}

	for _, rule := range m.rules {
		if matches := rule.Pattern.FindStringSubmatch(text); matches != nil {
			if actions := rule.ActionBuilder(matches); len(actions) > 0 {
				return actions
			}
		}
	}
	return nil
}
