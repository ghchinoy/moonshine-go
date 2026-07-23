package event

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/session"
)

func TestFromUpdate_Basic(t *testing.T) {
	u := session.Update{
		Transcript: moonshine.Transcript{Lines: []moonshine.Line{
			{ID: 1, Text: "hello", IsComplete: true},
			{ID: 2, Text: "wor", IsComplete: false},
		}},
		TTFT:        250 * time.Millisecond,
		Elapsed:     1500 * time.Millisecond,
		PollLatency: 12 * time.Millisecond,
		FinalizedLines: []session.LineTiming{
			{ID: 1, TimeToFinal: 500 * time.Millisecond},
		},
	}

	ev := FromUpdate(u)

	if len(ev.Lines) != 2 {
		t.Fatalf("Lines len = %d, want 2", len(ev.Lines))
	}
	if got, want := ev.TTFTms, int64(250); got != want {
		t.Errorf("TTFTms = %d, want %d", got, want)
	}
	if got, want := ev.ElapsedMs, int64(1500); got != want {
		t.Errorf("ElapsedMs = %d, want %d", got, want)
	}
	if got, want := ev.PollLatencyMs, int64(12); got != want {
		t.Errorf("PollLatencyMs = %d, want %d", got, want)
	}
	if len(ev.FinalizedLineIDs) != 1 || ev.FinalizedLineIDs[0] != 1 {
		t.Errorf("FinalizedLineIDs = %v, want [1]", ev.FinalizedLineIDs)
	}
	if ev.Err != "" {
		t.Errorf("Err = %q, want empty", ev.Err)
	}
	if ev.Done {
		t.Errorf("Done = true, want false")
	}
}

func TestFromUpdate_ErrAndDone(t *testing.T) {
	u := session.Update{Err: errors.New("boom"), Done: false}
	ev := FromUpdate(u)
	if ev.Err != "boom" {
		t.Errorf("Err = %q, want %q", ev.Err, "boom")
	}

	u2 := session.Update{
		Done: true,
		Summary: &session.SessionSummary{
			LinesFinalized:    3,
			AvgTimeToFinal:    time.Second,
			MaxTimeToFinal:    2 * time.Second,
			AvgRevisions:      1.5,
			MaxRevisions:      4,
			AvgStabilityRatio: 0.75,
		},
	}
	ev2 := FromUpdate(u2)
	if !ev2.Done {
		t.Fatalf("Done = false, want true")
	}
	if ev2.Summary == nil {
		t.Fatalf("Summary is nil, want non-nil")
	}
	if ev2.Summary.LinesFinalized != 3 {
		t.Errorf("Summary.LinesFinalized = %d, want 3", ev2.Summary.LinesFinalized)
	}
	if ev2.Summary.AvgTimeToFinalMs != 1000 {
		t.Errorf("Summary.AvgTimeToFinalMs = %d, want 1000", ev2.Summary.AvgTimeToFinalMs)
	}
	if ev2.Summary.MaxTimeToFinalMs != 2000 {
		t.Errorf("Summary.MaxTimeToFinalMs = %d, want 2000", ev2.Summary.MaxTimeToFinalMs)
	}
}

func TestTranscriptEvent_FinalizedLines(t *testing.T) {
	ev := TranscriptEvent{
		Lines: []moonshine.Line{
			{ID: 1, Text: "one", IsComplete: true},
			{ID: 2, Text: "two", IsComplete: true},
			{ID: 3, Text: "thr", IsComplete: false},
		},
		FinalizedLineIDs: []uint64{2},
	}
	got := ev.FinalizedLines()
	if len(got) != 1 || got[0].ID != 2 || got[0].Text != "two" {
		t.Fatalf("FinalizedLines() = %+v, want [{ID:2 Text:two}]", got)
	}
}

func TestTranscriptEvent_FinalizedLines_Empty(t *testing.T) {
	ev := TranscriptEvent{Lines: []moonshine.Line{{ID: 1, Text: "x"}}}
	if got := ev.FinalizedLines(); got != nil {
		t.Errorf("FinalizedLines() = %v, want nil", got)
	}
}

func TestActionRequest_JSONRoundTrip(t *testing.T) {
	args, err := json.Marshal(SpeakArgs{Text: "hello", Voice: "kokoro_default", Speed: 1.2})
	if err != nil {
		t.Fatal(err)
	}
	req := ActionRequest{ID: "abc123", Verb: "speak", Args: args}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ActionRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != req.ID || got.Verb != req.Verb {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, req)
	}
	var speak SpeakArgs
	if err := json.Unmarshal(got.Args, &speak); err != nil {
		t.Fatalf("unmarshal Args: %v", err)
	}
	if speak.Text != "hello" || speak.Voice != "kokoro_default" || speak.Speed != 1.2 {
		t.Errorf("SpeakArgs = %+v, want {hello kokoro_default 1.2}", speak)
	}
}

func TestActionResult_JSONShape(t *testing.T) {
	ok := ActionResult{ID: "x", OK: true}
	raw, _ := json.Marshal(ok)
	if got := string(raw); got != `{"id":"x","ok":true}` {
		t.Errorf("ActionResult{OK:true} JSON = %s", got)
	}

	fail := ActionResult{ID: "y", OK: false, Err: "unknown verb"}
	raw2, _ := json.Marshal(fail)
	if got := string(raw2); got != `{"id":"y","ok":false,"err":"unknown verb"}` {
		t.Errorf("ActionResult{OK:false} JSON = %s", got)
	}
}

func TestDisplayCard_JSONRoundTrip(t *testing.T) {
	card := DisplayCard{
		Title: "Weather",
		Body:  "72F and sunny",
		Kind:  "lookup",
		Data:  json.RawMessage(`{"temp_f":72}`),
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	var got DisplayCard
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != card.Title || got.Body != card.Body || got.Kind != card.Kind {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, card)
	}
}
