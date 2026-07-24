package serveapi

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTranscriptEventJSONOmitEmptyLines(t *testing.T) {
	ev := TranscriptEvent{ElapsedMs: 100}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if s == `{"lines":null,"elapsed_ms":100}` {
		t.Errorf("JSON should not serialize nil lines as null: %s", s)
	}
}

func TestTranscriptEventFinalizedLines(t *testing.T) {
	ev := TranscriptEvent{
		Lines: []Line{
			{ID: 1, Text: "one", IsComplete: true},
			{ID: 2, Text: "two", IsComplete: true},
			{ID: 3, Text: "interim"},
		},
		FinalizedLineIDs: []uint64{2},
	}
	got := ev.FinalizedLines()
	if len(got) != 1 || got[0].ID != 2 || got[0].Text != "two" {
		t.Fatalf("FinalizedLines() = %+v, want single line id=2", got)
	}

	if ev2 := (TranscriptEvent{Lines: ev.Lines}); ev2.FinalizedLines() != nil {
		t.Fatalf("no FinalizedLineIDs should yield nil, got %+v", ev2.FinalizedLines())
	}
}

func TestTranscriptEventJSONTags(t *testing.T) {
	// Guard the wire format: a snapshot of the expected JSON keys.
	b, err := json.Marshal(TranscriptEvent{
		Lines:            []Line{{ID: 1, Text: "hi", IsComplete: true}},
		FinalizedLineIDs: []uint64{1},
		ElapsedMs:        10,
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"lines", "finalized_line_ids", "elapsed_ms"} {
		if _, ok := m[k]; !ok {
			t.Errorf("expected JSON key %q in %s", k, b)
		}
	}
	// Omitempty fields absent when zero.
	if _, ok := m["ttft_ms"]; ok {
		t.Errorf("ttft_ms should be omitted when zero: %s", b)
	}
}

func TestCompositeHandlerFirstNonEmptyWins(t *testing.T) {
	empty := handlerFunc(func(context.Context, Line) []ActionRequest { return nil })
	speak := handlerFunc(func(context.Context, Line) []ActionRequest {
		return []ActionRequest{{Verb: "speak"}}
	})
	display := handlerFunc(func(context.Context, Line) []ActionRequest {
		return []ActionRequest{{Verb: "display"}}
	})
	c := NewCompositeHandler(nil, empty, speak, display)
	got := c.OnFinalizedLine(context.Background(), Line{ID: 1})
	if len(got) != 1 || got[0].Verb != "speak" {
		t.Fatalf("composite = %+v, want first non-empty (speak)", got)
	}
}

func TestAgentRunnerDedupAndDispatch(t *testing.T) {
	var mu sync.Mutex
	var dispatched []ActionRequest
	done := make(chan struct{}, 2)
	sink := ActionSinkFunc(func(ctx context.Context, req ActionRequest) (ActionResult, error) {
		mu.Lock()
		dispatched = append(dispatched, req)
		mu.Unlock()
		done <- struct{}{}
		return ActionResult{ID: req.ID, OK: true}, nil
	})
	handler := handlerFunc(func(ctx context.Context, l Line) []ActionRequest {
		return []ActionRequest{{Verb: "speak", ID: l.Text}}
	})
	r := NewAgentRunner(handler, sink)

	ev := TranscriptEvent{
		Lines:            []Line{{ID: 7, Text: "hello", IsComplete: true}},
		FinalizedLineIDs: []uint64{7},
	}
	r.ProcessEvent(context.Background(), ev)
	r.ProcessEvent(context.Background(), ev) // same line again -> must dedupe

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for dispatch")
	}

	mu.Lock()
	count := len(dispatched)
	mu.Unlock()

	if count != 1 {
		t.Fatalf("expected 1 dispatch after dedup, got %d (%+v)", count, dispatched)
	}
}

func TestAgentRunnerIgnoresInterim(t *testing.T) {
	called := false
	handler := handlerFunc(func(context.Context, Line) []ActionRequest {
		called = true
		return nil
	})
	r := NewAgentRunner(handler, nil)
	r.ProcessLine(context.Background(), Line{ID: 1, IsComplete: false})
	if called {
		t.Fatal("handler must not be called for interim (incomplete) lines")
	}
}

func TestAgentRunner_DecoupledDispatch(t *testing.T) {
	dispatchStarted := make(chan struct{})
	var startOnce sync.Once
	dispatchDone := make(chan struct{})

	sink := ActionSinkFunc(func(ctx context.Context, req ActionRequest) (ActionResult, error) {
		startOnce.Do(func() { close(dispatchStarted) })
		<-dispatchDone
		return ActionResult{ID: req.ID, OK: true}, nil
	})

	handler := handlerFunc(func(ctx context.Context, l Line) []ActionRequest {
		return []ActionRequest{{Verb: "speak", ID: l.Text}}
	})

	r := NewAgentRunner(handler, sink)

	// Channel with buffer capacity 1
	events := make(chan TranscriptEvent, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go r.Run(ctx, events)

	// Feed first event that triggers Dispatch
	events <- TranscriptEvent{
		Lines:            []Line{{ID: 1, Text: "first", IsComplete: true}},
		FinalizedLineIDs: []uint64{1},
	}

	// Wait until Dispatch is entered and blocked
	select {
	case <-dispatchStarted:
	case <-ctx.Done():
		t.Fatal("timed out waiting for Dispatch to start")
	}

	// Feed 5 more events into the small channel buffer while Dispatch is still blocked.
	// If Run was synchronous, the 2nd send would block forever.
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		for id := uint64(2); id <= 5; id++ {
			events <- TranscriptEvent{
				Lines:            []Line{{ID: id, Text: "next", IsComplete: true}},
				FinalizedLineIDs: []uint64{id},
			}
		}
	}()

	select {
	case <-feedDone:
		// Success! Feeding events did not deadlock on blocked Dispatch.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("deadlock detected: event channel blocked while Dispatch was in progress")
	}

	// Unblock Dispatch and clean up
	close(dispatchDone)
	close(events)
}

func TestStaticRetriever(t *testing.T) {
	r := NewStaticRetriever(
		Result{Title: "Aspirin", Snippet: "pain reliever"},
		Result{Title: "Ibuprofen", Snippet: "NSAID"},
	)
	got, err := r.Retrieve(context.Background(), "nsaid")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Ibuprofen" {
		t.Fatalf("Retrieve(nsaid) = %+v, want Ibuprofen", got)
	}
	none, _ := r.Retrieve(context.Background(), "")
	if len(none) != 0 {
		t.Fatalf("empty query should return no results, got %d", len(none))
	}
}

func TestNoopRetriever(t *testing.T) {
	got, err := NoopRetriever{}.Retrieve(context.Background(), "anything")
	if err != nil || got != nil {
		t.Fatalf("NoopRetriever = (%v, %v), want (nil, nil)", got, err)
	}
}

// fakeSource verifies the AudioSource contract (Chunks + Err) is usable.
type fakeSource struct {
	ch  chan []float32
	err error
}

func (f *fakeSource) Chunks() <-chan []float32 { return f.ch }
func (f *fakeSource) Err() error               { return f.err }

func TestAudioSourceContract(t *testing.T) {
	f := &fakeSource{ch: make(chan []float32, 1)}
	var _ AudioSource = f // interface satisfaction

	f.ch <- []float32{0.1, 0.2}
	close(f.ch)
	f.err = errors.New("connection dropped")

	var got [][]float32
	for c := range f.Chunks() {
		got = append(got, c)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(got))
	}
	if f.Err() == nil {
		t.Fatal("expected non-nil Err after abnormal termination")
	}
}

func TestRemoteAudioSource_Basic(t *testing.T) {
	src := NewRemoteAudioSource(AudioFormat{
		SampleRate: 16000,
		Channels:   1,
		Encoding:   AudioEncodingFloat32,
	}, 5)

	var _ AudioSource = src

	ctx := context.Background()
	samples := []float32{0.1, 0.2, 0.3}
	if err := src.WriteSamples(ctx, samples); err != nil {
		t.Fatalf("WriteSamples error: %v", err)
	}

	select {
	case got := <-src.Chunks():
		if len(got) != len(samples) {
			t.Fatalf("chunk len = %d, want %d", len(got), len(samples))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for chunk")
	}

	if err := src.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if src.Err() != nil {
		t.Errorf("expected nil Err after clean Close, got %v", src.Err())
	}
}

type handlerFunc func(context.Context, Line) []ActionRequest

func (h handlerFunc) OnFinalizedLine(ctx context.Context, l Line) []ActionRequest {
	return h(ctx, l)
}

func TestLineSpeakerLabel(t *testing.T) {
	if got := (Line{}).SpeakerLabel(); got != "" {
		t.Errorf("no SpeakerSpans: SpeakerLabel() = %q, want \"\"", got)
	}
	l := Line{SpeakerSpans: []SpeakerSpan{
		{SpeakerIndex: 1},
		{SpeakerIndex: 0},
		{SpeakerIndex: 1}, // duplicate index, should not repeat in output
	}}
	if got, want := l.SpeakerLabel(), "S0+S1"; got != want {
		t.Errorf("SpeakerLabel() = %q, want %q", got, want)
	}
}

func TestLineWordTimingsSummary(t *testing.T) {
	if got := (Line{}).WordTimingsSummary(); got != "" {
		t.Errorf("no Words: WordTimingsSummary() = %q, want \"\"", got)
	}
	l := Line{Words: []Word{
		{Text: "hello", Start: 0.1},
		{Text: "world", Start: 0.5},
	}}
	if got, want := l.WordTimingsSummary(), "hello@0.10 world@0.50"; got != want {
		t.Errorf("WordTimingsSummary() = %q, want %q", got, want)
	}
}
