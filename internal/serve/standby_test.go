package serve

import (
	"context"
	"encoding/json"
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
