// Package event defines the transport-agnostic wire types for the
// moonshine serve sidecar: outbound events (transcript updates, display
// cards) and inbound actions (speak, display, session control). These types
// are the frozen contract between the Hub/transports (WebSocket, gRPC) and
// the agent layer -- see docs/serve-sidecar.md section 8 before renaming
// any field.
package event

import (
	"encoding/json"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/session"
)

// Kind discriminates the payload carried by an Event when it is serialized
// for a transport that doesn't have native sum types (e.g. a single JSON
// object per WebSocket text frame). gRPC's oneof-based Event message
// (serve.proto, added by the gRPC transport task) mirrors this same set.
type Kind string

const (
	KindTranscript   Kind = "transcript"
	KindDisplay      Kind = "display"
	KindActionResult Kind = "action_result"
)

// TranscriptEvent is the JSON/proto-friendly projection of a session.Update:
// every line currently known (interim + finalized), which of them finalized
// on this specific poll, and the session's running timing stats.
//
// Consumers MUST treat this as delta-tolerant, not frame-complete: the Hub
// drops interim updates under backpressure (mirroring session.send,
// internal/session/session.go:252), so a subscriber may miss an
// intermediate TranscriptEvent. What it must never miss is a finalized
// line, and the Hub's dedup guarantees that: dedupe on Line.ID (only lines
// present in FinalizedLineIDs on the update where they first flip
// IsComplete=true count as "newly finalized" for that delivery).
type TranscriptEvent struct {
	// Lines mirrors the full current transcript snapshot: interim lines
	// plus all previously finalized lines. Reuses moonshine.Line directly
	// (already JSON-tagged; see internal/moonshine/stt.go) rather than
	// redefining an equivalent struct.
	Lines []moonshine.Line `json:"lines"`

	// FinalizedLineIDs holds the IDs of lines that transitioned to
	// IsComplete on this specific poll (usually 0 or 1 entries; see
	// session.Update.FinalizedLines). Look them up in Lines by ID for
	// their finalized text. Consumers that only care about "a new
	// finalized utterance happened" should trigger on this field, not on
	// scanning Lines for IsComplete (which includes lines finalized on
	// earlier polls too).
	FinalizedLineIDs []uint64 `json:"finalized_line_ids,omitempty"`

	// TTFTms is time-to-first-token in milliseconds (time from session
	// start to the first non-empty line of text), or 0 until that
	// happens. Mirrors session.Update.TTFT.
	TTFTms int64 `json:"ttft_ms,omitempty"`
	// ElapsedMs is time since the session started, in milliseconds.
	ElapsedMs int64 `json:"elapsed_ms"`
	// PollLatencyMs is how long the most recent Transcribe() call took,
	// in milliseconds. Mirrors session.Update.PollLatency.
	PollLatencyMs int64 `json:"poll_latency_ms,omitempty"`

	// Summary aggregates line-finalization stats across the whole
	// session. Only set on the final (Done) event.
	Summary *SessionSummary `json:"summary,omitempty"`

	// Done is true on the final event of the session.
	Done bool `json:"done,omitempty"`
	// Err is a non-empty error message if this update carried an error
	// (session.Update.Err, stringified so the wire format stays plain
	// JSON/proto scalars).
	Err string `json:"err,omitempty"`
}

// SessionSummary mirrors session.SessionSummary for the wire format
// (duration fields as milliseconds/floats rather than time.Duration, which
// doesn't have a canonical JSON/proto encoding).
type SessionSummary struct {
	LinesFinalized    int     `json:"lines_finalized"`
	AvgTimeToFinalMs  int64   `json:"avg_time_to_final_ms"`
	MaxTimeToFinalMs  int64   `json:"max_time_to_final_ms"`
	AvgRevisions      float64 `json:"avg_revisions"`
	MaxRevisions      int     `json:"max_revisions"`
	AvgStabilityRatio float64 `json:"avg_stability_ratio"`
}

// FromUpdate converts a session.Update into the wire-format TranscriptEvent.
// This is the single place that bridges the internal session package to the
// serve/event wire types -- keep session.Update's shape and this mapping in
// sync when either changes.
func FromUpdate(u session.Update) TranscriptEvent {
	ev := TranscriptEvent{
		Lines:         u.Transcript.Lines,
		TTFTms:        u.TTFT.Milliseconds(),
		ElapsedMs:     u.Elapsed.Milliseconds(),
		PollLatencyMs: u.PollLatency.Milliseconds(),
		Done:          u.Done,
	}
	if u.Err != nil {
		ev.Err = u.Err.Error()
	}
	if len(u.FinalizedLines) > 0 {
		ev.FinalizedLineIDs = make([]uint64, len(u.FinalizedLines))
		for i, lt := range u.FinalizedLines {
			ev.FinalizedLineIDs[i] = lt.ID
		}
	}
	if u.Summary != nil {
		ev.Summary = &SessionSummary{
			LinesFinalized:    u.Summary.LinesFinalized,
			AvgTimeToFinalMs:  u.Summary.AvgTimeToFinal.Milliseconds(),
			MaxTimeToFinalMs:  u.Summary.MaxTimeToFinal.Milliseconds(),
			AvgRevisions:      u.Summary.AvgRevisions,
			MaxRevisions:      u.Summary.MaxRevisions,
			AvgStabilityRatio: u.Summary.AvgStabilityRatio,
		}
	}
	return ev
}

// FinalizedLines returns the moonshine.Line values from ev.Lines whose ID
// appears in ev.FinalizedLineIDs -- i.e. the lines that newly finalized on
// this specific update. Convenience for agent/consumer code that would
// otherwise have to build the same ID-set lookup themselves.
func (ev TranscriptEvent) FinalizedLines() []moonshine.Line {
	if len(ev.FinalizedLineIDs) == 0 {
		return nil
	}
	want := make(map[uint64]bool, len(ev.FinalizedLineIDs))
	for _, id := range ev.FinalizedLineIDs {
		want[id] = true
	}
	var out []moonshine.Line
	for _, l := range ev.Lines {
		if want[l.ID] {
			out = append(out, l)
		}
	}
	return out
}

// DisplayCard is a small, structured piece of information the agent (or an
// external subscriber via the "display" action) wants shown in a UI: e.g. a
// lookup result, a confirmation, or a status card. Kind is a free-form hint
// for renderers (e.g. "info", "lookup", "error"); Data carries
// kind-specific structured payload, left as raw JSON so new kinds don't
// require changing this struct.
type DisplayCard struct {
	Title string          `json:"title"`
	Body  string          `json:"body,omitempty"`
	Kind  string          `json:"kind,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// ActionRequest is an inbound instruction from a subscriber (or the
// in-process agent) to the Dispatcher. Verb selects the handler; Args is
// verb-specific and left as raw JSON so new verbs don't require changing
// this struct. ID is an opaque client-chosen correlation token echoed back
// in the matching ActionResult (empty if the caller doesn't need
// correlation).
//
// Known verbs (see docs/serve-sidecar.md section 8 and the Dispatcher
// task): "speak", "display", "session.pause", "session.resume",
// "session.stop", "agent.result". Unknown verbs must produce an
// ActionResult{OK:false} rather than being silently dropped.
type ActionRequest struct {
	ID   string          `json:"id,omitempty"`
	Verb string          `json:"verb"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ActionResult is the Dispatcher's response to an ActionRequest, delivered
// back to the requesting transport (and, for transports that broadcast,
// potentially to other subscribers as a KindActionResult event).
type ActionResult struct {
	ID  string `json:"id,omitempty"`
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
}

// SpeakArgs is the Args payload for the "speak" verb.
type SpeakArgs struct {
	Text  string  `json:"text"`
	Voice string  `json:"voice,omitempty"`
	Speed float64 `json:"speed,omitempty"`
}

// DisplayArgs is the Args payload for the "display" verb: a DisplayCard to
// fan out to subscribers.
type DisplayArgs = DisplayCard
