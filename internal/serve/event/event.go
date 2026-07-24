// Package event bridges the internal session/moonshine types to the public
// pkg/serveapi wire types. The types themselves (TranscriptEvent,
// ActionRequest, ActionResult, DisplayCard, SpeakArgs, Kind) are aliases of
// pkg/serveapi -- this package owns no type definitions of its own, only the
// FromUpdate conversion, which is the one place that needs to depend on
// internal/session (and, transitively, internal/audio/cgo). See
// docs/vision/serveapi-design.md for why the split is shaped this way.
package event

import (
	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/session"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// Kind, TranscriptEvent, SessionSummary, DisplayCard, ActionRequest,
// ActionResult, and SpeakArgs are aliases of the identically-named
// pkg/serveapi types: the public package is the source of truth, this
// package is just where the session/moonshine-dependent conversion lives.
type (
	Kind            = serveapi.Kind
	TranscriptEvent = serveapi.TranscriptEvent
	SessionSummary  = serveapi.SessionSummary
	DisplayCard     = serveapi.DisplayCard
	ActionRequest   = serveapi.ActionRequest
	ActionResult    = serveapi.ActionResult
	SpeakArgs       = serveapi.SpeakArgs
	TTSAudioEvent   = serveapi.TTSAudioEvent
)

// DisplayArgs is the Args payload for the "display" verb: a DisplayCard to
// fan out to subscribers.
type DisplayArgs = serveapi.DisplayCard

const (
	KindTranscript   = serveapi.KindTranscript
	KindDisplay      = serveapi.KindDisplay
	KindActionResult = serveapi.KindActionResult
	KindTTSAudio     = serveapi.KindTTSAudio
)

// FromUpdate converts a session.Update into the wire-format TranscriptEvent.
// By default, raw PCM AudioData is omitted from lines for privacy and wire-size
// efficiency (see FromUpdateWithAudio to include raw audio).
func FromUpdate(u session.Update) TranscriptEvent {
	return FromUpdateWithAudio(u, false)
}

// FromUpdateWithAudio converts a session.Update into the wire-format
// TranscriptEvent, optionally preserving raw PCM AudioData on each line.
func FromUpdateWithAudio(u session.Update, includeAudio bool) TranscriptEvent {
	ev := TranscriptEvent{
		Lines:         linesFromMoonshine(u.Transcript.Lines, includeAudio),
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

// lineFromMoonshine converts a moonshine.Line (the internal, native-bound
// transcript line) into the public serveapi.Line shadow struct. Field-for-field
// mapping; keep in sync with both types.
func lineFromMoonshine(l moonshine.Line, includeAudio bool) serveapi.Line {
	out := serveapi.Line{
		Text:                l.Text,
		StartTime:           l.StartTime,
		Duration:            l.Duration,
		ID:                  l.ID,
		IsComplete:          l.IsComplete,
		IsUpdated:           l.IsUpdated,
		IsNew:               l.IsNew,
		HasTextChanged:      l.HasTextChanged,
		HaveSpeakersChanged: l.HaveSpeakersChanged,
		LastLatencyMs:       l.LastLatencyMs,
		Confidence:          l.Confidence,
	}
	if includeAudio {
		out.AudioData = l.AudioData
	}
	if len(l.Words) > 0 {
		out.Words = make([]serveapi.Word, len(l.Words))
		for i, w := range l.Words {
			out.Words[i] = serveapi.Word{
				Text:       w.Text,
				Start:      w.Start,
				End:        w.End,
				Confidence: w.Confidence,
			}
		}
		if out.Confidence == 0 {
			out.Confidence = out.MeanConfidence()
		}
	}
	if len(l.SpeakerSpans) > 0 {
		out.SpeakerSpans = make([]serveapi.SpeakerSpan, len(l.SpeakerSpans))
		for i, s := range l.SpeakerSpans {
			out.SpeakerSpans[i] = serveapi.SpeakerSpan{
				StartTime:    s.StartTime,
				Duration:     s.Duration,
				SpeakerID:    s.SpeakerID,
				SpeakerIndex: s.SpeakerIndex,
				StartChar:    s.StartChar,
				EndChar:      s.EndChar,
			}
		}
	}
	return out
}

// linesFromMoonshine converts a slice of moonshine.Line to serveapi.Line.
func linesFromMoonshine(ls []moonshine.Line, includeAudio bool) []serveapi.Line {
	if len(ls) == 0 {
		return nil
	}
	out := make([]serveapi.Line, len(ls))
	for i, l := range ls {
		out[i] = lineFromMoonshine(l, includeAudio)
	}
	return out
}
