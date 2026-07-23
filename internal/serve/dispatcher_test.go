package serve

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

// --- fakes ---

type fakeSpeaker struct {
	speaking  bool
	lastText  string
	lastVoice string
	lastSpeed float64
	err       error
}

func (f *fakeSpeaker) Speak(_ context.Context, text, voice string, speed float64) error {
	f.lastText, f.lastVoice, f.lastSpeed = text, voice, speed
	return f.err
}
func (f *fakeSpeaker) Speaking() bool { return f.speaking }

type fakePublisher struct{ published []any }

func (f *fakePublisher) Publish(ev any) { f.published = append(f.published, ev) }

type fakeSession struct {
	paused, resumed, stopped bool
	err                      error
}

func (f *fakeSession) Pause(context.Context) error  { f.paused = true; return f.err }
func (f *fakeSession) Resume(context.Context) error { f.resumed = true; return f.err }
func (f *fakeSession) Stop(context.Context) error   { f.stopped = true; return f.err }

func mustArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// --- tests ---

func TestDispatcher_UnknownVerb(t *testing.T) {
	d := NewDispatcher(nil, &fakePublisher{}, nil, true)
	res := d.Handle(context.Background(), event.ActionRequest{ID: "1", Verb: "nonsense"})
	if res.OK {
		t.Fatal("expected OK=false for unknown verb")
	}
	if res.ID != "1" {
		t.Errorf("ID = %q, want %q", res.ID, "1")
	}
	if res.Err == "" {
		t.Error("expected a non-empty Err message")
	}
}

func TestDispatcher_Speak_Success(t *testing.T) {
	sp := &fakeSpeaker{}
	d := NewDispatcher(sp, &fakePublisher{}, nil, true)
	req := event.ActionRequest{ID: "2", Verb: "speak", Args: mustArgs(t, event.SpeakArgs{Text: "hello", Voice: "v1", Speed: 1.5})}

	res := d.Handle(context.Background(), req)
	if !res.OK {
		t.Fatalf("expected OK, got Err=%q", res.Err)
	}
	if sp.lastText != "hello" || sp.lastVoice != "v1" || sp.lastSpeed != 1.5 {
		t.Errorf("Speak called with (%q,%q,%v), want (hello,v1,1.5)", sp.lastText, sp.lastVoice, sp.lastSpeed)
	}
}

func TestDispatcher_Speak_GatedByAllowActions(t *testing.T) {
	sp := &fakeSpeaker{}
	d := NewDispatcher(sp, &fakePublisher{}, nil, false) // allowActions=false
	req := event.ActionRequest{ID: "3", Verb: "speak", Args: mustArgs(t, event.SpeakArgs{Text: "hello"})}

	res := d.Handle(context.Background(), req)
	if res.OK {
		t.Fatal("expected OK=false when actions are disabled")
	}
	if sp.lastText != "" {
		t.Error("Speak should not have been called")
	}
}

func TestDispatcher_Speak_MissingText(t *testing.T) {
	sp := &fakeSpeaker{}
	d := NewDispatcher(sp, &fakePublisher{}, nil, true)
	req := event.ActionRequest{ID: "4", Verb: "speak", Args: mustArgs(t, event.SpeakArgs{})}

	res := d.Handle(context.Background(), req)
	if res.OK {
		t.Fatal("expected OK=false for missing text")
	}
}

func TestDispatcher_Speak_NoSpeakerConfigured(t *testing.T) {
	d := NewDispatcher(nil, &fakePublisher{}, nil, true)
	req := event.ActionRequest{ID: "5", Verb: "speak", Args: mustArgs(t, event.SpeakArgs{Text: "hi"})}

	res := d.Handle(context.Background(), req)
	if res.OK {
		t.Fatal("expected OK=false with no speaker configured")
	}
}

func TestDispatcher_Speak_PropagatesError(t *testing.T) {
	sp := &fakeSpeaker{err: errors.New("synth failed")}
	d := NewDispatcher(sp, &fakePublisher{}, nil, true)
	req := event.ActionRequest{ID: "6", Verb: "speak", Args: mustArgs(t, event.SpeakArgs{Text: "hi"})}

	res := d.Handle(context.Background(), req)
	if res.OK {
		t.Fatal("expected OK=false when Speak returns an error")
	}
	if res.Err != "synth failed" {
		t.Errorf("Err = %q, want %q", res.Err, "synth failed")
	}
}

func TestDispatcher_Display(t *testing.T) {
	pub := &fakePublisher{}
	d := NewDispatcher(nil, pub, nil, true)
	card := event.DisplayCard{Title: "Weather", Body: "sunny"}
	req := event.ActionRequest{ID: "7", Verb: "display", Args: mustArgs(t, card)}

	res := d.Handle(context.Background(), req)
	if !res.OK {
		t.Fatalf("expected OK, got Err=%q", res.Err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published %d events, want 1", len(pub.published))
	}
	got, ok := pub.published[0].(event.DisplayCard)
	if !ok || got.Title != "Weather" {
		t.Fatalf("published %#v, want DisplayCard{Title:Weather}", pub.published[0])
	}
}

func TestDispatcher_Display_NotGatedByAllowActions(t *testing.T) {
	// display is a read-only/informational verb, not a mutating action,
	// so it must work even when allowActions is false (unlike
	// speak/session control/run_command).
	pub := &fakePublisher{}
	d := NewDispatcher(nil, pub, nil, false)
	req := event.ActionRequest{ID: "8", Verb: "display", Args: mustArgs(t, event.DisplayCard{Title: "x"})}

	res := d.Handle(context.Background(), req)
	if !res.OK {
		t.Fatalf("expected OK, got Err=%q", res.Err)
	}
}

func TestDispatcher_Display_RequiresTitleOrBody(t *testing.T) {
	d := NewDispatcher(nil, &fakePublisher{}, nil, true)
	req := event.ActionRequest{ID: "9", Verb: "display", Args: mustArgs(t, event.DisplayCard{})}

	res := d.Handle(context.Background(), req)
	if res.OK {
		t.Fatal("expected OK=false for empty display card")
	}
}

func TestDispatcher_SessionControl_AllVerbs(t *testing.T) {
	sess := &fakeSession{}
	d := NewDispatcher(nil, &fakePublisher{}, sess, true)

	for _, verb := range []string{"session.pause", "session.resume", "session.stop"} {
		res := d.Handle(context.Background(), event.ActionRequest{ID: verb, Verb: verb})
		if !res.OK {
			t.Fatalf("%s: expected OK, got Err=%q", verb, res.Err)
		}
	}
	if !sess.paused || !sess.resumed || !sess.stopped {
		t.Errorf("session state = %+v, want all true", sess)
	}
}

func TestDispatcher_SessionControl_GatedByAllowActions(t *testing.T) {
	sess := &fakeSession{}
	d := NewDispatcher(nil, &fakePublisher{}, sess, false)

	res := d.Handle(context.Background(), event.ActionRequest{Verb: "session.pause"})
	if res.OK {
		t.Fatal("expected OK=false when actions are disabled")
	}
	if sess.paused {
		t.Error("Pause should not have been called")
	}
}

func TestDispatcher_SessionControl_NoSessionConfigured(t *testing.T) {
	d := NewDispatcher(nil, &fakePublisher{}, nil, true)
	res := d.Handle(context.Background(), event.ActionRequest{Verb: "session.stop"})
	if res.OK {
		t.Fatal("expected OK=false with no session control configured")
	}
}

func TestDispatcher_AgentResult_DisplayAndSpeak(t *testing.T) {
	sp := &fakeSpeaker{}
	pub := &fakePublisher{}
	d := NewDispatcher(sp, pub, nil, true)

	args := AgentResultArgs{
		Display: &event.DisplayCard{Title: "Lookup", Body: "42"},
		Speak:   "the answer is 42",
	}
	res := d.Handle(context.Background(), event.ActionRequest{Verb: "agent.result", Args: mustArgs(t, args)})
	if !res.OK {
		t.Fatalf("expected OK, got Err=%q", res.Err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published %d events, want 1", len(pub.published))
	}
	if sp.lastText != "the answer is 42" {
		t.Errorf("Speak text = %q, want %q", sp.lastText, "the answer is 42")
	}
}

func TestDispatcher_AgentResult_SpeakGatedByAllowActions(t *testing.T) {
	sp := &fakeSpeaker{}
	d := NewDispatcher(sp, &fakePublisher{}, nil, false)

	args := AgentResultArgs{Speak: "should not speak"}
	res := d.Handle(context.Background(), event.ActionRequest{Verb: "agent.result", Args: mustArgs(t, args)})
	if res.OK {
		t.Fatal("expected OK=false when actions are disabled and Speak is set")
	}
	if sp.lastText != "" {
		t.Error("Speak should not have been called")
	}
}

func TestDispatcher_AgentResult_DisplayOnlyWorksWithoutAllowActions(t *testing.T) {
	pub := &fakePublisher{}
	d := NewDispatcher(nil, pub, nil, false)

	args := AgentResultArgs{Display: &event.DisplayCard{Title: "info"}}
	res := d.Handle(context.Background(), event.ActionRequest{Verb: "agent.result", Args: mustArgs(t, args)})
	if !res.OK {
		t.Fatalf("expected OK (display-only agent.result should not require allowActions), got Err=%q", res.Err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published %d events, want 1", len(pub.published))
	}
}

func TestDispatcher_RegisterVerb_OverridesBuiltin(t *testing.T) {
	d := NewDispatcher(nil, &fakePublisher{}, nil, true)
	called := false
	d.RegisterVerb("display", func(_ context.Context, req event.ActionRequest) event.ActionResult {
		called = true
		return event.ActionResult{ID: req.ID, OK: true}
	})

	res := d.Handle(context.Background(), event.ActionRequest{ID: "custom", Verb: "display"})
	if !called {
		t.Fatal("custom handler was not invoked")
	}
	if !res.OK {
		t.Fatalf("expected OK, got Err=%q", res.Err)
	}
}

func TestDispatcher_RegisterVerb_NewVerb(t *testing.T) {
	d := NewDispatcher(nil, &fakePublisher{}, nil, true)
	d.RegisterVerb("run_command", func(_ context.Context, req event.ActionRequest) event.ActionResult {
		return event.ActionResult{ID: req.ID, OK: true}
	})

	res := d.Handle(context.Background(), event.ActionRequest{ID: "rc", Verb: "run_command"})
	if !res.OK {
		t.Fatalf("expected OK, got Err=%q", res.Err)
	}
}

func TestDispatcher_AllowActions_Accessor(t *testing.T) {
	d := NewDispatcher(nil, &fakePublisher{}, nil, true)
	if !d.AllowActions() {
		t.Error("AllowActions() = false, want true")
	}
	d2 := NewDispatcher(nil, &fakePublisher{}, nil, false)
	if d2.AllowActions() {
		t.Error("AllowActions() = true, want false")
	}
}

var _ Speaker = (*fakeSpeaker)(nil)
var _ Publisher = (*fakePublisher)(nil)
var _ SessionControl = (*fakeSession)(nil)
