package serveapi

import (
	"fmt"
	"sort"
	"strings"
)

// Line is one "line" (roughly a sentence or phrase) of a transcript. For
// streaming results, IsComplete distinguishes finalized lines from the (at
// most one) trailing in-progress line.
//
// This is a shadow of internal/moonshine.Line: the field set and JSON tags
// match the internal type exactly, so the wire format is unchanged, but the
// public contract does not depend on the internal package.
type Line struct {
	Text                string        `json:"text"`
	AudioData           []float32     `json:"audio_data,omitempty"`
	StartTime           float32       `json:"start_time"`
	Duration            float32       `json:"duration"`
	ID                  uint64        `json:"id"`
	IsComplete          bool          `json:"is_complete"`
	IsUpdated           bool          `json:"is_updated"`
	IsNew               bool          `json:"is_new"`
	HasTextChanged      bool          `json:"has_text_changed"`
	HaveSpeakersChanged bool          `json:"have_speakers_changed"`
	LastLatencyMs       uint32        `json:"last_latency_ms"`
	Words               []Word        `json:"words,omitempty"`
	SpeakerSpans        []SpeakerSpan `json:"speaker_spans,omitempty"`
}

// SpeakerLabel returns a compact summary of which speaker(s) contributed to
// this line, e.g. "S0" or "S0+S1", derived from SpeakerSpans (only populated
// when the "identify_speakers" option is enabled). Returns "" if there are
// no speaker spans -- either identify_speakers wasn't enabled, or no speech
// has been attributed to a speaker yet.
func (l Line) SpeakerLabel() string {
	if len(l.SpeakerSpans) == 0 {
		return ""
	}
	seen := map[uint32]bool{}
	indices := make([]uint32, 0, len(l.SpeakerSpans))
	for _, s := range l.SpeakerSpans {
		if !seen[s.SpeakerIndex] {
			seen[s.SpeakerIndex] = true
			indices = append(indices, s.SpeakerIndex)
		}
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })
	parts := make([]string, len(indices))
	for i, idx := range indices {
		parts[i] = fmt.Sprintf("S%d", idx)
	}
	return strings.Join(parts, "+")
}

// WordTimingsSummary renders each word with its start time as a compact
// "word@start word@start ..." string. Returns "" if Words is empty (word
// timestamps weren't enabled, or none have arrived yet).
func (l Line) WordTimingsSummary() string {
	if len(l.Words) == 0 {
		return ""
	}
	parts := make([]string, len(l.Words))
	for i, w := range l.Words {
		parts[i] = fmt.Sprintf("%s@%.2f", w.Text, w.Start)
	}
	return strings.Join(parts, " ")
}

// Word is a single word with timing information. Only populated when the
// "word_timestamps" option is enabled on the transcriber.
type Word struct {
	Text       string  `json:"text"`
	Start      float32 `json:"start"`
	End        float32 `json:"end"`
	Confidence float32 `json:"confidence"`
}

// SpeakerSpan is one contiguous span of speech attributed to a single
// speaker. Only populated when the "identify_speakers" option is enabled.
type SpeakerSpan struct {
	StartTime    float32 `json:"start_time"`
	Duration     float32 `json:"duration"`
	SpeakerID    uint64  `json:"speaker_id"`
	SpeakerIndex uint32  `json:"speaker_index"`
	StartChar    uint64  `json:"start_char"`
	EndChar      uint64  `json:"end_char"`
}
