package session

import (
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
)

// newTestLive builds a Live with just enough state for trackLines/summarize
// to work, without a real transcriber/mic (which trackLines never touches).
func newTestLive() *Live {
	return &Live{tracked: make(map[uint64]*lineProgress)}
}

func TestTrackLines_RevisionsAndStability(t *testing.T) {
	l := newTestLive()

	// Poll 1: line first appears, incomplete.
	got := l.trackLines(moonshine.Transcript{Lines: []moonshine.Line{
		{ID: 1, Text: "It was", IsComplete: false},
	}})
	if len(got) != 0 {
		t.Fatalf("poll 1: expected no finalized lines, got %v", got)
	}

	// Poll 2: unchanged text, still incomplete -- a "stable" observation.
	got = l.trackLines(moonshine.Transcript{Lines: []moonshine.Line{
		{ID: 1, Text: "It was", IsComplete: false},
	}})
	if len(got) != 0 {
		t.Fatalf("poll 2: expected no finalized lines, got %v", got)
	}

	// Poll 3: text revised, still incomplete -- one revision.
	got = l.trackLines(moonshine.Transcript{Lines: []moonshine.Line{
		{ID: 1, Text: "It was the best of times", IsComplete: false},
	}})
	if len(got) != 0 {
		t.Fatalf("poll 3: expected no finalized lines, got %v", got)
	}

	// Poll 4: finalizes, text unchanged from poll 3.
	got = l.trackLines(moonshine.Transcript{Lines: []moonshine.Line{
		{ID: 1, Text: "It was the best of times", IsComplete: true},
	}})
	if len(got) != 1 {
		t.Fatalf("poll 4: expected 1 finalized line, got %d: %v", len(got), got)
	}
	lt := got[0]
	if lt.ID != 1 {
		t.Errorf("ID = %d, want 1", lt.ID)
	}
	if lt.PollCount != 2 {
		t.Errorf("PollCount = %d, want 2 (polls 2 and 3, after first appearance)", lt.PollCount)
	}
	if lt.Revisions != 1 {
		t.Errorf("Revisions = %d, want 1 (only poll 3 changed the text)", lt.Revisions)
	}
	wantStability := 1 - float64(1)/float64(2)
	if lt.StabilityRatio != wantStability {
		t.Errorf("StabilityRatio = %v, want %v", lt.StabilityRatio, wantStability)
	}
	if lt.TimeToFinal < 0 {
		t.Errorf("TimeToFinal = %v, want >= 0", lt.TimeToFinal)
	}

	if len(l.finalized) != 1 {
		t.Fatalf("l.finalized has %d entries, want 1", len(l.finalized))
	}

	// Poll 5: the same line reappearing already-complete (e.g. still in the
	// transcript history) must not be double-counted.
	got = l.trackLines(moonshine.Transcript{Lines: []moonshine.Line{
		{ID: 1, Text: "It was the best of times", IsComplete: true},
	}})
	if len(got) != 0 {
		t.Fatalf("poll 5: expected no newly finalized lines (already done), got %v", got)
	}
	if len(l.finalized) != 1 {
		t.Fatalf("l.finalized has %d entries after re-seeing a complete line, want still 1 (no double count)", len(l.finalized))
	}
}

func TestTrackLines_FinalizesOnFirstSighting(t *testing.T) {
	l := newTestLive()

	got := l.trackLines(moonshine.Transcript{Lines: []moonshine.Line{
		{ID: 42, Text: "Ever tried.", IsComplete: true},
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 finalized line, got %d", len(got))
	}
	lt := got[0]
	if lt.PollCount != 0 {
		t.Errorf("PollCount = %d, want 0 (finalized on first sighting)", lt.PollCount)
	}
	if lt.Revisions != 0 {
		t.Errorf("Revisions = %d, want 0", lt.Revisions)
	}
	if lt.StabilityRatio != 1 {
		t.Errorf("StabilityRatio = %v, want 1 (nothing to compare against)", lt.StabilityRatio)
	}
	if lt.TimeToFinal < 0 {
		t.Errorf("TimeToFinal = %v, want >= 0", lt.TimeToFinal)
	}
}

func TestTrackLines_IgnoresEmptyText(t *testing.T) {
	l := newTestLive()

	got := l.trackLines(moonshine.Transcript{Lines: []moonshine.Line{
		{ID: 7, Text: "", IsComplete: false},
	}})
	if len(got) != 0 {
		t.Fatalf("expected no finalized lines, got %v", got)
	}
	if _, tracked := l.tracked[7]; tracked {
		t.Errorf("line with empty text should not start tracking")
	}
}

func TestTrackLines_MultipleLinesFinalizeInOnePoll(t *testing.T) {
	l := newTestLive()

	l.trackLines(moonshine.Transcript{Lines: []moonshine.Line{
		{ID: 1, Text: "First", IsComplete: false},
		{ID: 2, Text: "Second", IsComplete: false},
	}})
	got := l.trackLines(moonshine.Transcript{Lines: []moonshine.Line{
		{ID: 1, Text: "First", IsComplete: true},
		{ID: 2, Text: "Second", IsComplete: true},
	}})
	if len(got) != 2 {
		t.Fatalf("expected 2 finalized lines in one poll, got %d", len(got))
	}
}

func TestSummarize(t *testing.T) {
	if s := summarize(nil); s.LinesFinalized != 0 {
		t.Errorf("summarize(nil).LinesFinalized = %d, want 0", s.LinesFinalized)
	}

	finalized := []LineTiming{
		{ID: 1, TimeToFinal: 500 * time.Millisecond, Revisions: 0, StabilityRatio: 1.0},
		{ID: 2, TimeToFinal: 1500 * time.Millisecond, Revisions: 4, StabilityRatio: 0.2},
	}
	s := summarize(finalized)
	if s.LinesFinalized != 2 {
		t.Errorf("LinesFinalized = %d, want 2", s.LinesFinalized)
	}
	if want := 1000 * time.Millisecond; s.AvgTimeToFinal != want {
		t.Errorf("AvgTimeToFinal = %v, want %v", s.AvgTimeToFinal, want)
	}
	if want := 1500 * time.Millisecond; s.MaxTimeToFinal != want {
		t.Errorf("MaxTimeToFinal = %v, want %v", s.MaxTimeToFinal, want)
	}
	if want := 2.0; s.AvgRevisions != want {
		t.Errorf("AvgRevisions = %v, want %v", s.AvgRevisions, want)
	}
	if want := 4; s.MaxRevisions != want {
		t.Errorf("MaxRevisions = %d, want %d", s.MaxRevisions, want)
	}
	if want := 0.6; s.AvgStabilityRatio != want {
		t.Errorf("AvgStabilityRatio = %v, want %v", s.AvgStabilityRatio, want)
	}
}
