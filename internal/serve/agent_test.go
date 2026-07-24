package serve_test

import (
	"context"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/serve"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

type fakeAgentHandler struct {
	fn func(line serveapi.Line) []event.ActionRequest
}

func (f *fakeAgentHandler) OnFinalizedLine(ctx context.Context, line serveapi.Line) []event.ActionRequest {
	if f.fn != nil {
		return f.fn(line)
	}
	return nil
}

func TestExternalAgent(t *testing.T) {
	var agent serve.ExternalAgent
	actions := agent.OnFinalizedLine(context.Background(), serveapi.Line{ID: 1, Text: "hello", IsComplete: true})
	if len(actions) != 0 {
		t.Fatalf("expected no actions from ExternalAgent, got %v", actions)
	}
}

func TestCompositeHandler(t *testing.T) {
	h1 := &fakeAgentHandler{fn: func(line serveapi.Line) []event.ActionRequest {
		if line.Text == "first" {
			return []event.ActionRequest{{Verb: "h1"}}
		}
		return nil
	}}

	h2 := &fakeAgentHandler{fn: func(line serveapi.Line) []event.ActionRequest {
		if line.Text == "second" {
			return []event.ActionRequest{{Verb: "h2"}}
		}
		return nil
	}}

	comp := serve.NewCompositeHandler(h1, h2)
	ctx := context.Background()

	t.Run("first handler matches", func(t *testing.T) {
		res := comp.OnFinalizedLine(ctx, serveapi.Line{ID: 1, Text: "first", IsComplete: true})
		if len(res) != 1 || res[0].Verb != "h1" {
			t.Fatalf("expected h1 action, got %v", res)
		}
	})

	t.Run("second handler matches", func(t *testing.T) {
		res := comp.OnFinalizedLine(ctx, serveapi.Line{ID: 2, Text: "second", IsComplete: true})
		if len(res) != 1 || res[0].Verb != "h2" {
			t.Fatalf("expected h2 action, got %v", res)
		}
	})

	t.Run("no handler matches", func(t *testing.T) {
		res := comp.OnFinalizedLine(ctx, serveapi.Line{ID: 3, Text: "none", IsComplete: true})
		if len(res) != 0 {
			t.Fatalf("expected no action, got %v", res)
		}
	})
}

func TestAgentRunner_DeduplicationAndDispatch(t *testing.T) {
	dispatched := make([]event.ActionRequest, 0)
	sink := serve.ActionSinkFunc(func(ctx context.Context, req event.ActionRequest) (event.ActionResult, error) {
		dispatched = append(dispatched, req)
		return event.ActionResult{OK: true}, nil
	})

	handler := &fakeAgentHandler{fn: func(line serveapi.Line) []event.ActionRequest {
		return []event.ActionRequest{{ID: "action-1", Verb: "speak"}}
	}}

	runner := serve.NewAgentRunner(handler, sink)
	ctx := context.Background()

	line1 := serveapi.Line{ID: 100, Text: "first line", IsComplete: true}
	line1Interim := serveapi.Line{ID: 100, Text: "first line (interim)", IsComplete: false}

	// 1. Process incomplete line -> should ignore
	runner.ProcessLine(ctx, line1Interim)
	if len(dispatched) != 0 {
		t.Fatalf("expected 0 dispatched actions for incomplete line, got %d", len(dispatched))
	}

	// 2. Process complete line -> should dispatch 1 action
	runner.ProcessLine(ctx, line1)
	if len(dispatched) != 1 {
		t.Fatalf("expected 1 dispatched action, got %d", len(dispatched))
	}

	// 3. Process same complete line again -> should deduplicate and ignore
	runner.ProcessLine(ctx, line1)
	if len(dispatched) != 1 {
		t.Fatalf("expected deduplication (still 1 dispatched action), got %d", len(dispatched))
	}

	// 4. Process event carrying the same finalized line
	ev := event.TranscriptEvent{
		Lines:            []serveapi.Line{line1},
		FinalizedLineIDs: []uint64{100},
	}
	runner.ProcessEvent(ctx, ev)
	if len(dispatched) != 1 {
		t.Fatalf("expected event processing to deduplicate line 100, got %d dispatched", len(dispatched))
	}
}

func TestAgentRunner_RunLoop(t *testing.T) {
	dispatched := make(chan string, 10)
	sink := serve.ActionSinkFunc(func(ctx context.Context, req event.ActionRequest) (event.ActionResult, error) {
		dispatched <- req.Verb
		return event.ActionResult{OK: true}, nil
	})

	handler := &fakeAgentHandler{fn: func(line serveapi.Line) []event.ActionRequest {
		return []event.ActionRequest{{Verb: line.Text}}
	}}

	runner := serve.NewAgentRunner(handler, sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan event.TranscriptEvent, 5)
	go runner.Run(ctx, ch)

	line := serveapi.Line{ID: 200, Text: "say_hello", IsComplete: true}
	ch <- event.TranscriptEvent{
		Lines:            []serveapi.Line{line},
		FinalizedLineIDs: []uint64{200},
	}

	select {
	case verb := <-dispatched:
		if verb != "say_hello" {
			t.Fatalf("expected verb 'say_hello', got %q", verb)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for runner to dispatch action")
	}

	close(ch)
}
