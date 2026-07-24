package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

// Speaker is the narrow interface Dispatcher needs to fulfil the "speak"
// verb. The real implementation (tts.go's TTSSpeaker) wraps a
// moonshine.Synthesizer and audio.PlayFloat32; tests use a fake.
type Speaker interface {
	// Speak synthesizes and plays text, blocking until playback finishes
	// (or ctx is cancelled -- see TTSSpeaker's doc comment for the extent
	// to which cancellation is honored). voice/speed are passed through
	// as moonshine TTS options ("voice", "speed") when non-empty/non-zero.
	// pub, if non-nil, receives TTSAudioEvent wire events for this utterance.
	Speak(ctx context.Context, pub Publisher, text, voice string, speed float64) error
	// Speaking reports whether a Speak call is currently in progress, so
	// callers (notably the mic-feed barge-in guard) can suppress
	// self-transcription while the sidecar's own voice is playing.
	Speaking() bool
}

// SessionControl is the narrow interface Dispatcher needs to fulfil the
// "session.pause"/"session.resume"/"session.stop" verbs. The concrete
// implementation is supplied by cmd/moonshine/serve.go (P6), which owns
// the actual session.Live/mic lifecycle; Dispatcher only needs to be able
// to ask for a state transition.
type SessionControl interface {
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Handler is a verb handler function, as registered via
// Dispatcher.RegisterVerb. It receives the raw ActionRequest so it can
// decode its own Args shape.
type Handler func(ctx context.Context, req event.ActionRequest) event.ActionResult

// Dispatcher routes inbound event.ActionRequest values (from a transport
// subscriber, or from the in-process agent) to the appropriate handler:
// built-in verbs (speak, display, session.pause/resume/stop, agent.result)
// or verbs registered later by other packages via RegisterVerb (e.g. the
// Gemini agent's "run_command" tool, added without needing to modify this
// file -- see docs/serve-sidecar.md's file-ownership map).
//
// Dispatcher is safe for concurrent use.
type Dispatcher struct {
	speaker      Speaker
	publisher    Publisher
	session      SessionControl
	allowActions bool

	mu     sync.RWMutex
	custom map[string]Handler
}

// NewDispatcher builds a Dispatcher. speaker and session may be nil if
// speak/session-control verbs are not needed (they will then fail with a
// clear error rather than panicking). publisher is used for the "display"
// verb and must not be nil. allowActions gates the built-in mutating verbs
// (speak, session.pause, session.resume, session.stop); when false, those
// verbs return ActionResult{OK:false} without side effects. Custom
// handlers registered via RegisterVerb are responsible for checking
// AllowActions themselves if they perform similarly sensitive operations
// (e.g. a future "run_command" verb).
func NewDispatcher(speaker Speaker, publisher Publisher, session SessionControl, allowActions bool) *Dispatcher {
	return &Dispatcher{
		speaker:      speaker,
		publisher:    publisher,
		session:      session,
		allowActions: allowActions,
		custom:       make(map[string]Handler),
	}
}

// AllowActions reports whether this Dispatcher was constructed with
// mutating actions enabled, for custom handlers that need to enforce the
// same gate.
func (d *Dispatcher) AllowActions() bool { return d.allowActions }

// RegisterVerb adds (or replaces) a handler for verb. Intended for other
// packages (e.g. the Gemini agent's tool-calling loop) to extend the set of
// dispatchable verbs without editing this file. Registering a verb that
// collides with a built-in (speak, display, session.pause, session.resume,
// session.stop, agent.result) overrides the built-in.
func (d *Dispatcher) RegisterVerb(verb string, h Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.custom[verb] = h
}

// Handle routes req to the appropriate handler and returns its result.
// Unknown verbs return ActionResult{OK:false} with a descriptive Err
// rather than being silently dropped.
func (d *Dispatcher) Handle(ctx context.Context, req event.ActionRequest) event.ActionResult {
	d.mu.RLock()
	custom, hasCustom := d.custom[req.Verb]
	d.mu.RUnlock()
	if hasCustom {
		return custom(ctx, req)
	}

	switch req.Verb {
	case "speak":
		return d.handleSpeak(ctx, req)
	case "display":
		return d.handleDisplay(req)
	case "session.pause", "session.resume", "session.stop", "session.barge_in":
		return d.handleSessionControl(ctx, req)
	case "agent.result":
		return d.handleAgentResult(ctx, req)
	default:
		return fail(req.ID, fmt.Sprintf("unknown verb %q", req.Verb))
	}
}

func (d *Dispatcher) handleSpeak(ctx context.Context, req event.ActionRequest) event.ActionResult {
	if !d.allowActions {
		return fail(req.ID, "actions are disabled (--allow-actions not set)")
	}
	if d.speaker == nil {
		return fail(req.ID, "no speaker configured")
	}
	var args event.SpeakArgs
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return fail(req.ID, "invalid speak args: "+err.Error())
		}
	}
	if args.Text == "" {
		return fail(req.ID, "speak: text is required")
	}
	if err := d.speaker.Speak(ctx, d.publisher, args.Text, args.Voice, args.Speed); err != nil {
		return fail(req.ID, err.Error())
	}
	return ok(req.ID)
}

func (d *Dispatcher) handleDisplay(req event.ActionRequest) event.ActionResult {
	var card event.DisplayCard
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &card); err != nil {
			return fail(req.ID, "invalid display args: "+err.Error())
		}
	}
	if card.Title == "" && card.Body == "" {
		return fail(req.ID, "display: title or body is required")
	}
	d.publisher.Publish(card)
	return ok(req.ID)
}

func (d *Dispatcher) handleSessionControl(ctx context.Context, req event.ActionRequest) event.ActionResult {
	if !d.allowActions {
		return fail(req.ID, "actions are disabled (--allow-actions not set)")
	}
	if d.session == nil {
		return fail(req.ID, "no session control configured")
	}
	var err error
	switch req.Verb {
	case "session.pause":
		err = d.session.Pause(ctx)
	case "session.resume":
		err = d.session.Resume(ctx)
	case "session.stop":
		err = d.session.Stop(ctx)
	case "session.barge_in":
		if interrupter, ok := d.speaker.(interface{ Interrupt(context.Context) }); ok {
			interrupter.Interrupt(ctx)
		}
		if d.session != nil {
			err = d.session.Pause(ctx)
		}
	}
	if err != nil {
		return fail(req.ID, err.Error())
	}
	return ok(req.ID)
}

// AgentResultArgs is the Args payload for the "agent.result" verb: a
// subscriber (or the in-process agent) reporting the outcome of some
// out-of-band work, optionally asking the sidecar to display and/or speak
// it. Both fields are optional; at least one should normally be set.
type AgentResultArgs struct {
	Display *event.DisplayCard `json:"display,omitempty"`
	Speak   string             `json:"speak,omitempty"`
}

func (d *Dispatcher) handleAgentResult(ctx context.Context, req event.ActionRequest) event.ActionResult {
	var args AgentResultArgs
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return fail(req.ID, "invalid agent.result args: "+err.Error())
		}
	}
	if args.Display != nil {
		d.publisher.Publish(*args.Display)
	}
	if args.Speak != "" {
		if !d.allowActions {
			return fail(req.ID, "actions are disabled (--allow-actions not set)")
		}
		if d.speaker == nil {
			return fail(req.ID, "no speaker configured")
		}
		if err := d.speaker.Speak(ctx, d.publisher, args.Speak, "", 0); err != nil {
			return fail(req.ID, err.Error())
		}
	}
	return ok(req.ID)
}

func ok(id string) event.ActionResult { return event.ActionResult{ID: id, OK: true} }
func fail(id, msg string) event.ActionResult {
	return event.ActionResult{ID: id, OK: false, Err: msg}
}
