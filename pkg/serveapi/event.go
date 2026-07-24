package serveapi

import "encoding/json"

// Kind discriminates the payload carried by an event when it is serialized
// for a transport that doesn't have native sum types (e.g. a single JSON
// object per WebSocket text frame). The gRPC transport's oneof-based Event
// message mirrors this same set.
type Kind string

const (
	KindTranscript   Kind = "transcript"
	KindDisplay      Kind = "display"
	KindActionResult Kind = "action_result"
	KindTTSAudio     Kind = "tts_audio"
)

// TranscriptEvent is the JSON/proto-friendly projection of a live
// transcription update: every line currently known (interim + finalized),
// which of them finalized on this specific poll, and the session's running
// timing stats.
//
// Consumers MUST treat this as delta-tolerant, not frame-complete: the sidecar
// drops interim updates under backpressure, so a subscriber may miss an
// intermediate TranscriptEvent. What it must never miss is a finalized line:
// dedupe on Line.ID, and treat only IDs present in FinalizedLineIDs as "newly
// finalized" for a given event.
type TranscriptEvent struct {
	// Lines is the full current transcript snapshot: interim lines plus all
	// previously finalized lines.
	Lines []Line `json:"lines,omitempty"`

	// FinalizedLineIDs holds the IDs of lines that transitioned to complete
	// on this specific poll (usually 0 or 1 entries). Look them up in Lines
	// by ID for their finalized text. Consumers that only care that "a new
	// finalized utterance happened" should trigger on this field, not on
	// scanning Lines for IsComplete (which also includes lines finalized on
	// earlier polls).
	FinalizedLineIDs []uint64 `json:"finalized_line_ids,omitempty"`

	// TTFTms is time-to-first-token in milliseconds (session start to the
	// first non-empty line of text), or 0 until that happens.
	TTFTms int64 `json:"ttft_ms,omitempty"`
	// ElapsedMs is time since the session started, in milliseconds.
	ElapsedMs int64 `json:"elapsed_ms"`
	// PollLatencyMs is how long the most recent transcription poll took, in
	// milliseconds.
	PollLatencyMs int64 `json:"poll_latency_ms,omitempty"`

	// Summary aggregates line-finalization stats across the whole session.
	// Only set on the final (Done) event.
	Summary *SessionSummary `json:"summary,omitempty"`

	// Done is true on the final event of the session.
	Done bool `json:"done,omitempty"`
	// Err is a non-empty error message if this update carried an error.
	Err string `json:"err,omitempty"`
}

// FinalizedLines returns the Line values from ev.Lines whose ID appears in
// ev.FinalizedLineIDs -- i.e. the lines that newly finalized on this specific
// event. Convenience for consumer code that would otherwise build the same
// ID-set lookup itself.
func (ev TranscriptEvent) FinalizedLines() []Line {
	if len(ev.FinalizedLineIDs) == 0 {
		return nil
	}
	want := make(map[uint64]bool, len(ev.FinalizedLineIDs))
	for _, id := range ev.FinalizedLineIDs {
		want[id] = true
	}
	var out []Line
	for _, l := range ev.Lines {
		if want[l.ID] {
			out = append(out, l)
		}
	}
	return out
}

// SessionSummary aggregates line-finalization stats across a whole session.
// Duration fields are milliseconds (not time.Duration, which has no canonical
// JSON/proto encoding).
type SessionSummary struct {
	LinesFinalized    int     `json:"lines_finalized"`
	AvgTimeToFinalMs  int64   `json:"avg_time_to_final_ms"`
	MaxTimeToFinalMs  int64   `json:"max_time_to_final_ms"`
	AvgRevisions      float64 `json:"avg_revisions"`
	MaxRevisions      int     `json:"max_revisions"`
	AvgStabilityRatio float64 `json:"avg_stability_ratio"`
}

// TTSAudioEvent carries synthesized speech audio bytes or state signals to
// remote clients over transports for hosted use.
type TTSAudioEvent struct {
	ID         string    `json:"id,omitempty"`          // ActionRequest correlation ID, if triggered by a speak action
	Text       string    `json:"text,omitempty"`        // Synthesized text
	AudioData  []float32 `json:"audio_data,omitempty"`  // PCM float32 samples
	SampleRate int       `json:"sample_rate,omitempty"` // e.g. 24000 or 16000
	State      string    `json:"state"`                 // "start", "chunk", "end", or "interrupted"
}

// DisplayCard is a small, structured piece of information to show in a UI:
// e.g. a lookup result, a confirmation, or a status card. Kind is a free-form
// hint for renderers (e.g. "info", "lookup", "error"); Data carries
// kind-specific structured payload as raw JSON so new kinds don't require
// changing this struct.
type DisplayCard struct {
	Title string          `json:"title"`
	Body  string          `json:"body,omitempty"`
	Kind  string          `json:"kind,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// ActionRequest is an inbound instruction from a subscriber (or the in-process
// agent) to the sidecar. Verb selects the handler; Args is verb-specific and
// left as raw JSON so new verbs don't require changing this struct. ID is an
// opaque client-chosen correlation token echoed back in the matching
// ActionResult (empty if the caller doesn't need correlation).
//
// Known verbs: "speak", "display", "session.pause", "session.resume",
// "session.stop", "agent.result". Unknown verbs must produce an
// ActionResult{OK:false} rather than being silently dropped.
type ActionRequest struct {
	ID   string          `json:"id,omitempty"`
	Verb string          `json:"verb"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ActionResult is the sidecar's response to an ActionRequest.
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

// DisplayArgs is the Args payload for the "display" verb: a DisplayCard to fan
// out to subscribers.
type DisplayArgs = DisplayCard
