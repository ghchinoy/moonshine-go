package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/internal/session"
	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
)

func TestMatchWakePhrase(t *testing.T) {
	phrases := []string{"resume listening", "start listening", "wake up"}

	tests := []struct {
		input       string
		wantMatched bool
		wantPhrase  string
	}{
		{"resume listening", true, "resume listening"},
		{"Resume Listening.", true, "resume listening"},
		{"START LISTENING!", true, "start listening"},
		{"wake up please", true, "wake up"},
		{"Hey computer, wake up", true, "wake up"},
		{"Please resume listening now", true, "resume listening"},
		{"what time is it?", false, ""},
		{"stop listening", false, ""},
		{"resuming listening", false, ""},
		{"", false, ""},
	}

	for _, tt := range tests {
		matched, ok := matchWakePhrase(tt.input, phrases)
		if ok != tt.wantMatched {
			t.Errorf("matchWakePhrase(%q) ok = %v, want %v", tt.input, ok, tt.wantMatched)
		}
		if ok && matched != tt.wantPhrase {
			t.Errorf("matchWakePhrase(%q) phrase = %q, want %q", tt.input, matched, tt.wantPhrase)
		}
	}
}

func TestLiveSessionControl_Modes(t *testing.T) {
	ctrl := &LiveSessionControl{}

	// Initial
	if ctrl.IsPaused() {
		t.Error("new ctrl should not be paused")
	}
	if ctrl.MuteCapture() {
		t.Error("new ctrl should not mute capture")
	}
	if len(ctrl.PassthroughPhrases()) != 0 {
		t.Error("new ctrl should have no passthrough phrases")
	}

	// Hard pause (Pause)
	_ = ctrl.Pause(context.Background())
	if !ctrl.IsPaused() {
		t.Error("after Pause(), IsPaused should be true")
	}
	if !ctrl.MuteCapture() {
		t.Error("after Pause(), MuteCapture should be true (hard mute)")
	}
	if len(ctrl.PassthroughPhrases()) != 0 {
		t.Error("hard pause should have no passthrough phrases")
	}

	// Resume
	_ = ctrl.Resume(context.Background())
	if ctrl.IsPaused() {
		t.Error("after Resume(), IsPaused should be false")
	}
	if ctrl.MuteCapture() {
		t.Error("after Resume(), MuteCapture should be false")
	}

	// Standby pause (PauseWith)
	_ = ctrl.PauseWith(context.Background(), []string{"resume listening", "wake up"})
	if !ctrl.IsPaused() {
		t.Error("after PauseWith(), IsPaused should be true")
	}
	if ctrl.MuteCapture() {
		t.Error("after PauseWith(), MuteCapture should be false (standby)")
	}
	phrases := ctrl.PassthroughPhrases()
	if len(phrases) != 2 || phrases[0] != "resume listening" || phrases[1] != "wake up" {
		t.Errorf("unexpected PassthroughPhrases: %v", phrases)
	}

	// Resume from standby
	_ = ctrl.Resume(context.Background())
	if ctrl.IsPaused() {
		t.Error("after Resume(), IsPaused should be false")
	}
	if len(ctrl.PassthroughPhrases()) != 0 {
		t.Error("after Resume(), PassthroughPhrases should be empty")
	}
}

func TestFilterStandbyUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrl := &LiveSessionControl{}
	hub := NewHub()
	subID, eventsCh := hub.Subscribe()
	defer hub.Unsubscribe(subID)

	inCh := make(chan session.Update, 10)
	outCh := filterStandbyUpdates(ctx, inCh, ctrl, hub)

	// 1. Unpaused: updates pass through
	u1 := session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{{ID: 1, Text: "Hello world", IsComplete: false}},
		},
	}
	inCh <- u1
	select {
	case got := <-outCh:
		if len(got.Transcript.Lines) != 1 || got.Transcript.Lines[0].Text != "Hello world" {
			t.Errorf("u1: unexpected passed update: %+v", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("u1: timed out waiting for unpaused pass-through")
	}

	// 2. Hard pause: all updates dropped
	_ = ctrl.Pause(ctx)
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{{ID: 2, Text: "Talking during mute", IsComplete: true}},
		},
		FinalizedLines: []session.LineTiming{{ID: 2}},
	}
	select {
	case got := <-outCh:
		t.Fatalf("hard pause: expected update to be dropped, got %+v", got)
	case <-time.After(100 * time.Millisecond):
		// OK, dropped
	}

	// 3. Standby mode: wake phrases configured
	_ = ctrl.PauseWith(ctx, []string{"resume listening"})

	// 3a. Interim update should be dropped
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{{ID: 3, Text: "resume", IsComplete: false}},
		},
	}
	select {
	case got := <-outCh:
		t.Fatalf("standby: expected interim update to be dropped, got %+v", got)
	case <-time.After(100 * time.Millisecond):
		// OK, dropped
	}

	// 3b. Non-matching finalized update should be dropped
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{{ID: 4, Text: "What's the weather today?", IsComplete: true}},
		},
		FinalizedLines: []session.LineTiming{{ID: 4}},
	}
	select {
	case got := <-outCh:
		t.Fatalf("standby: expected non-matching final update to be dropped, got %+v", got)
	case <-time.After(100 * time.Millisecond):
		// OK, dropped
	}

	// 3c. Matching finalized update should resume and pass through
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{{ID: 5, Text: "Resume listening.", IsComplete: true}},
		},
		FinalizedLines: []session.LineTiming{{ID: 5}},
	}
	select {
	case got := <-outCh:
		if len(got.Transcript.Lines) != 1 || got.Transcript.Lines[0].Text != "Resume listening." {
			t.Errorf("u5: unexpected passed update: %+v", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("u5: timed out waiting for wake phrase resume")
	}

	// Verify session unpaused
	if ctrl.IsPaused() {
		t.Error("expected ctrl to be unpaused after wake phrase match")
	}

	// Verify DisplayCard published
	select {
	case ev := <-eventsCh:
		card, ok := ev.(event.DisplayCard)
		if !ok || card.Title != "Listening Resumed" || card.Kind != "session" {
			t.Errorf("expected DisplayCard{Kind: 'session', Title: 'Listening Resumed'}, got %#v", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for resume DisplayCard")
	}
}

type fakeControlWithPauseWith struct {
	lastPauseWith []string
	paused        bool
	resumed       bool
}

func (f *fakeControlWithPauseWith) Pause(_ context.Context) error {
	f.paused = true
	f.lastPauseWith = nil
	return nil
}

func (f *fakeControlWithPauseWith) PauseWith(_ context.Context, p []string) error {
	f.paused = true
	f.lastPauseWith = p
	return nil
}

func (f *fakeControlWithPauseWith) Resume(_ context.Context) error {
	f.resumed = true
	f.paused = false
	return nil
}

func (f *fakeControlWithPauseWith) Stop(_ context.Context) error {
	return nil
}

func TestDispatcher_PauseArgs(t *testing.T) {
	fc := &fakeControlWithPauseWith{}
	d := NewDispatcher(nil, NewHub(), fc, true)

	// 1. Pause with explicit passthrough phrases
	args, _ := json.Marshal(event.PauseArgs{Passthrough: []string{"start", "wake up"}})
	res := d.Handle(context.Background(), event.ActionRequest{ID: "1", Verb: "session.pause", Args: args})
	if !res.OK {
		t.Errorf("res not OK: %s", res.Err)
	}
	if len(fc.lastPauseWith) != 2 || fc.lastPauseWith[0] != "start" || fc.lastPauseWith[1] != "wake up" {
		t.Errorf("unexpected lastPauseWith: %v", fc.lastPauseWith)
	}

	// 2. Pause with explicit empty array -> hard mute
	argsEmpty, _ := json.Marshal(event.PauseArgs{Passthrough: []string{}})
	res = d.Handle(context.Background(), event.ActionRequest{ID: "2", Verb: "session.pause", Args: argsEmpty})
	if !res.OK {
		t.Errorf("res not OK: %s", res.Err)
	}
	if len(fc.lastPauseWith) != 0 {
		t.Errorf("explicit empty passthrough should call hard Pause, got: %v", fc.lastPauseWith)
	}

	// 3. Pause with omitted args, but daemon has default wake phrases
	d.SetDefaultWakePhrases([]string{"default resume"})
	res = d.Handle(context.Background(), event.ActionRequest{ID: "3", Verb: "session.pause"})
	if !res.OK {
		t.Errorf("res not OK: %s", res.Err)
	}
	if len(fc.lastPauseWith) != 1 || fc.lastPauseWith[0] != "default resume" {
		t.Errorf("expected default wake phrases, got: %v", fc.lastPauseWith)
	}

	// 4. Pause with omitted args, no daemon default wake phrases -> hard mute
	d.SetDefaultWakePhrases(nil)
	res = d.Handle(context.Background(), event.ActionRequest{ID: "4", Verb: "session.pause"})
	if !res.OK {
		t.Errorf("res not OK: %s", res.Err)
	}
	if len(fc.lastPauseWith) != 0 {
		t.Errorf("expected hard mute, got: %v", fc.lastPauseWith)
	}
}

func TestFilterStandbyUpdates_MultiLineAmbientLeak(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrl := &LiveSessionControl{}
	hub := NewHub()
	subID, _ := hub.Subscribe()
	defer hub.Unsubscribe(subID)

	inCh := make(chan session.Update, 10)
	outCh := filterStandbyUpdates(ctx, inCh, ctrl, hub)

	// Line 1: unpaused speech
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{{ID: 1, Text: "The quick brown fox", IsComplete: true}},
		},
		FinalizedLines: []session.LineTiming{{ID: 1}},
	}
	select {
	case got := <-outCh:
		if len(got.Transcript.Lines) != 1 || got.Transcript.Lines[0].Text != "The quick brown fox" {
			t.Fatalf("unexpected unpaused update: %+v", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for unpaused line 1")
	}

	// Enter standby mode with wake phrase
	_ = ctrl.PauseWith(ctx, []string{"start listening"})

	// Line 2: spoken during standby (ambient / private) -> should be suppressed
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{
				{ID: 1, Text: "The quick brown fox", IsComplete: true},
				{ID: 2, Text: "This is confidential and private information", IsComplete: true},
			},
		},
		FinalizedLines: []session.LineTiming{{ID: 2}},
	}
	select {
	case got := <-outCh:
		t.Fatalf("standby line 2 should have been suppressed, got: %+v", got)
	case <-time.After(100 * time.Millisecond):
		// Dropped as expected
	}

	// Line 3: wake phrase spoken
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{
				{ID: 1, Text: "The quick brown fox", IsComplete: true},
				{ID: 2, Text: "This is confidential and private information", IsComplete: true},
				{ID: 3, Text: "Start listening.", IsComplete: true},
			},
		},
		FinalizedLines: []session.LineTiming{{ID: 3}},
	}
	select {
	case got := <-outCh:
		// On resume, Line 2 MUST be redacted!
		if len(got.Transcript.Lines) != 2 {
			t.Fatalf("expected 2 lines in resume update (1 and 3), got %d: %+v", len(got.Transcript.Lines), got.Transcript.Lines)
		}
		if got.Transcript.Lines[0].ID != 1 || got.Transcript.Lines[0].Text != "The quick brown fox" {
			t.Errorf("expected line 1, got %+v", got.Transcript.Lines[0])
		}
		if got.Transcript.Lines[1].ID != 3 || got.Transcript.Lines[1].Text != "Start listening." {
			t.Errorf("expected line 3, got %+v", got.Transcript.Lines[1])
		}
		for _, l := range got.Transcript.Lines {
			if l.ID == 2 {
				t.Errorf("CRITICAL PRIVACY LEAK: ambient line 2 found in resume transcript!")
			}
		}
		if len(got.FinalizedLines) != 1 || got.FinalizedLines[0].ID != 3 {
			t.Errorf("expected FinalizedLines to only have line 3, got: %+v", got.FinalizedLines)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for wake phrase resume update")
	}

	// Subsequent update while unpaused: Line 4 arrives
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{
				{ID: 1, Text: "The quick brown fox", IsComplete: true},
				{ID: 2, Text: "This is confidential and private information", IsComplete: true},
				{ID: 3, Text: "Start listening.", IsComplete: true},
				{ID: 4, Text: "Next command.", IsComplete: true},
			},
		},
		FinalizedLines: []session.LineTiming{{ID: 4}},
	}
	select {
	case got := <-outCh:
		if len(got.Transcript.Lines) != 3 {
			t.Fatalf("expected 3 lines in subsequent update (1, 3, 4), got %d: %+v", len(got.Transcript.Lines), got.Transcript.Lines)
		}
		for _, l := range got.Transcript.Lines {
			if l.ID == 2 {
				t.Errorf("CRITICAL PRIVACY LEAK: ambient line 2 persisted into subsequent update!")
			}
		}
		if got.Transcript.Lines[2].ID != 4 || got.Transcript.Lines[2].Text != "Next command." {
			t.Errorf("expected line 4, got %+v", got.Transcript.Lines[2])
		}
		if len(got.FinalizedLines) != 1 || got.FinalizedLines[0].ID != 4 {
			t.Errorf("expected FinalizedLines to have line 4, got: %+v", got.FinalizedLines)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for line 4 update")
	}
}

func TestFilterStandbyUpdates_SamePollAmbientAndWake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrl := &LiveSessionControl{}
	_ = ctrl.PauseWith(ctx, []string{"start listening"})

	inCh := make(chan session.Update, 5)
	outCh := filterStandbyUpdates(ctx, inCh, ctrl, nil)

	// Both ambient line 2 and wake line 3 finalize on the exact same poll
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{
				{ID: 2, Text: "ambient chatter in background", IsComplete: true},
				{ID: 3, Text: "start listening", IsComplete: true},
			},
		},
		FinalizedLines: []session.LineTiming{{ID: 2}, {ID: 3}},
	}

	select {
	case got := <-outCh:
		if len(got.Transcript.Lines) != 1 || got.Transcript.Lines[0].ID != 3 {
			t.Fatalf("expected only wake line 3 in Transcript.Lines, got: %+v", got.Transcript.Lines)
		}
		if len(got.FinalizedLines) != 1 || got.FinalizedLines[0].ID != 3 {
			t.Fatalf("expected only wake line 3 in FinalizedLines, got: %+v", got.FinalizedLines)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for resume update")
	}
}

func TestFilterStandbyUpdates_StraddlingIncompleteLine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrl := &LiveSessionControl{}
	inCh := make(chan session.Update, 10)
	outCh := filterStandbyUpdates(ctx, inCh, ctrl, nil)

	// Line 1 begins while unpaused but is incomplete
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{{ID: 1, Text: "Wait I was saying something", IsComplete: false}},
		},
	}
	<-outCh // interim pass-through

	// User pauses into standby
	_ = ctrl.PauseWith(ctx, []string{"start listening"})

	// Line 1 completes while paused
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{{ID: 1, Text: "Wait I was saying something private", IsComplete: true}},
		},
		FinalizedLines: []session.LineTiming{{ID: 1}},
	}
	// Should be dropped
	select {
	case got := <-outCh:
		t.Fatalf("expected straddling line completion during pause to be dropped, got: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}

	// Wake phrase spoken
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{
				{ID: 1, Text: "Wait I was saying something private", IsComplete: true},
				{ID: 2, Text: "start listening", IsComplete: true},
			},
		},
		FinalizedLines: []session.LineTiming{{ID: 2}},
	}

	select {
	case got := <-outCh:
		// Line 1 must NOT be in the resume transcript
		if len(got.Transcript.Lines) != 1 || got.Transcript.Lines[0].ID != 2 {
			t.Fatalf("expected only line 2 (wake phrase), got: %+v", got.Transcript.Lines)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for resume update")
	}
}

func TestFilterStandbyUpdates_HardMuteThenManualResume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrl := &LiveSessionControl{}
	inCh := make(chan session.Update, 10)
	outCh := filterStandbyUpdates(ctx, inCh, ctrl, nil)

	// Line 1 completed while unpaused
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{{ID: 1, Text: "First unpaused line", IsComplete: true}},
		},
		FinalizedLines: []session.LineTiming{{ID: 1}},
	}
	<-outCh

	// Hard mute
	_ = ctrl.Pause(ctx)

	// Line 2 completed during hard mute
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{
				{ID: 1, Text: "First unpaused line", IsComplete: true},
				{ID: 2, Text: "Secret during mute", IsComplete: true},
			},
		},
		FinalizedLines: []session.LineTiming{{ID: 2}},
	}
	select {
	case got := <-outCh:
		t.Fatalf("expected hard mute update to be dropped, got: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}

	// Manual resume (e.g. from UI or Enter key)
	_ = ctrl.Resume(ctx)

	// Line 3 completed after manual resume
	inCh <- session.Update{
		Transcript: moonshine.Transcript{
			Lines: []moonshine.Line{
				{ID: 1, Text: "First unpaused line", IsComplete: true},
				{ID: 2, Text: "Secret during mute", IsComplete: true},
				{ID: 3, Text: "Third line after resume", IsComplete: true},
			},
		},
		FinalizedLines: []session.LineTiming{{ID: 3}},
	}

	select {
	case got := <-outCh:
		if len(got.Transcript.Lines) != 2 {
			t.Fatalf("expected lines 1 and 3, got %d: %+v", len(got.Transcript.Lines), got.Transcript.Lines)
		}
		if got.Transcript.Lines[0].ID != 1 || got.Transcript.Lines[1].ID != 3 {
			t.Errorf("expected IDs [1, 3], got: [%d, %d]", got.Transcript.Lines[0].ID, got.Transcript.Lines[1].ID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for post-manual-resume update")
	}
}

func TestFilterStandbyUpdates_DoneAndErrWhilePaused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Done while hard muted
	ctrl := &LiveSessionControl{}
	_ = ctrl.Pause(ctx)
	inCh := make(chan session.Update, 5)
	outCh := filterStandbyUpdates(ctx, inCh, ctrl, nil)

	summary := &session.SessionSummary{LinesFinalized: 5}
	inCh <- session.Update{
		Done:    true,
		Summary: summary,
	}
	select {
	case got := <-outCh:
		if !got.Done {
			t.Errorf("expected Done=true, got: %+v", got)
		}
		if got.Summary == nil || got.Summary.LinesFinalized != 5 {
			t.Errorf("expected Summary preserved, got: %+v", got.Summary)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for Done update while paused")
	}

	// 2. Err while in standby mode
	ctrl2 := &LiveSessionControl{}
	_ = ctrl2.PauseWith(ctx, []string{"start listening"})
	inCh2 := make(chan session.Update, 5)
	outCh2 := filterStandbyUpdates(ctx, inCh2, ctrl2, nil)

	testErr := fmt.Errorf("audio device disconnected")
	inCh2 <- session.Update{
		Err: testErr,
	}
	select {
	case got := <-outCh2:
		if got.Err == nil || got.Err.Error() != testErr.Error() {
			t.Errorf("expected Err preserved, got: %+v", got.Err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for Err update while in standby")
	}
}
