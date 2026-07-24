// Package session orchestrates a live streaming transcription session: it
// owns a moonshine.Stream and an audio source, feeds audio in, polls for
// updated transcripts, and reports timing stats (notably time-to-first-token)
// that a CLI or TUI can render.
package session

import (
	"context"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// Update is emitted every time the session has a new (possibly unchanged)
// transcript to show, or the session has ended.
type Update struct {
	Transcript moonshine.Transcript
	// TTFT is the time from session start to the first non-empty line of
	// text, or zero until that happens.
	TTFT time.Duration
	// Elapsed is time since the session started.
	Elapsed time.Duration
	// PollLatency is how long the most recent Transcribe() call itself took
	// (distinct from moonshine's own last_transcription_latency_ms per
	// line, which measures model inference time specifically).
	PollLatency time.Duration
	// FinalizedLines holds a LineTiming for each line that transitioned to
	// IsComplete on this specific poll (usually zero or one entry, since
	// only the last line in a transcript may be incomplete at any time --
	// see moonshine-c-api.h -- but multiple can finalize in one poll if
	// several phrases completed between ticks). Empty on most updates.
	FinalizedLines []LineTiming
	// Summary aggregates LineTiming across every line finalized during the
	// whole session. Only set on the final Done update.
	Summary *SessionSummary
	// Done is true on the final update of the session (after Stop()).
	Done bool
	Err  error
}

// LineTiming captures one line's partial-transcript stability: how long it
// took from first appearing (with non-empty text) to being finalized
// (IsComplete), and how often its text was revised while still in
// progress. Only meaningful for streaming (live) sessions -- non-streaming
// transcribe never produces partial lines.
type LineTiming struct {
	// ID is the line's stable identifier (moonshine.Line.ID).
	ID uint64
	// TimeToFinal is wall-clock time from the line's first non-empty
	// appearance to the poll where IsComplete became true. Resolution is
	// bounded by the session's poll interval -- a value near zero means
	// the line appeared and finalized within the same poll cycle, not
	// that finalization was literally instantaneous.
	TimeToFinal time.Duration
	// PollCount is how many times, after its first appearance, this line
	// was re-observed while still incomplete -- i.e. how many
	// opportunities it had to change before finalizing. Zero if it
	// finalized on the very poll it first appeared.
	PollCount int
	// Revisions is how many of those re-observations actually changed the
	// line's text before it finalized.
	Revisions int
	// StabilityRatio is 1 - Revisions/PollCount (1.0 = text never changed
	// after its first appearance; lower = more "flickery"). 1.0 if
	// PollCount is 0 (finalized on first sighting, nothing to compare).
	StabilityRatio float64
}

// SessionSummary aggregates LineTiming across every line finalized during a
// Live session. Attached to the final (Done) Update. All fields besides
// LinesFinalized are zero-value/meaningless when LinesFinalized is 0 (no
// lines finalized, e.g. a session stopped before any speech completed) --
// check LinesFinalized before displaying or interpreting the rest.
type SessionSummary struct {
	LinesFinalized    int
	AvgTimeToFinal    time.Duration
	MaxTimeToFinal    time.Duration
	AvgRevisions      float64
	MaxRevisions      int
	AvgStabilityRatio float64
}

func summarize(finalized []LineTiming) *SessionSummary {
	s := &SessionSummary{LinesFinalized: len(finalized)}
	if len(finalized) == 0 {
		return s
	}
	var totalTTF time.Duration
	var totalRevisions int
	var totalStability float64
	for _, lt := range finalized {
		totalTTF += lt.TimeToFinal
		if lt.TimeToFinal > s.MaxTimeToFinal {
			s.MaxTimeToFinal = lt.TimeToFinal
		}
		totalRevisions += lt.Revisions
		if lt.Revisions > s.MaxRevisions {
			s.MaxRevisions = lt.Revisions
		}
		totalStability += lt.StabilityRatio
	}
	n := time.Duration(len(finalized))
	s.AvgTimeToFinal = totalTTF / n
	s.AvgRevisions = float64(totalRevisions) / float64(len(finalized))
	s.AvgStabilityRatio = totalStability / float64(len(finalized))
	return s
}

// lineProgress tracks one in-progress line's stability bookkeeping between
// polls, keyed by Line.ID in Live.tracked.
type lineProgress struct {
	firstSeen time.Time
	pollCount int
	revisions int
	lastText  string
	done      bool // true once its finalization has been recorded, to guard against double-counting if a complete line is somehow seen again
}

// Live runs a live audio -> streaming transcription session. Audio comes
// from a serveapi.AudioSource -- the local microphone by default, but
// anything satisfying that interface (e.g. a remote client's PCM stream)
// works equally well; the session doesn't know or care which.
type Live struct {
	stream       *moonshine.Stream
	source       serveapi.AudioSource
	pollInterval time.Duration
	updates      chan Update

	tracked   map[uint64]*lineProgress
	finalized []LineTiming
}

// NewLive creates and starts a streaming session against tr, consuming audio
// from source. pollInterval controls how often the (already internally rate
// limited) transcribe_stream call is polled; moonshine itself skips
// re-analysis of audio it's seen in the last ~200ms unless forced, so
// anything below that is wasted work.
func NewLive(tr *moonshine.Transcriber, source serveapi.AudioSource, pollInterval time.Duration) (*Live, error) {
	stream, err := tr.NewStream(0)
	if err != nil {
		return nil, err
	}
	if err := stream.Start(); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &Live{
		stream:       stream,
		source:       source,
		pollInterval: pollInterval,
		updates:      make(chan Update, 8),
		tracked:      make(map[uint64]*lineProgress),
	}, nil
}

// Updates returns the channel of transcript/stat updates. Closed when Run
// returns.
func (l *Live) Updates() <-chan Update { return l.updates }

// Run feeds mic audio into the stream and polls for transcripts until ctx is
// cancelled, then stops the stream and emits one final Done update. Run
// blocks; call it from a goroutine.
func (l *Live) Run(ctx context.Context) {
	defer close(l.updates)
	defer l.stream.Close()

	start := time.Now()
	var ttft time.Duration

	ticker := time.NewTicker(l.pollInterval)
	defer ticker.Stop()

	poll := func(flags uint32) {
		t0 := time.Now()
		transcript, err := l.stream.Transcribe(flags)
		latency := time.Since(t0)
		if err != nil {
			l.send(Update{Err: err, Elapsed: time.Since(start), PollLatency: latency})
			return
		}
		if ttft == 0 {
			if _, ok := transcript.FirstNonEmptyText(); ok {
				ttft = time.Since(start)
			}
		}
		finalized := l.trackLines(transcript)
		l.send(Update{Transcript: transcript, TTFT: ttft, Elapsed: time.Since(start), PollLatency: latency, FinalizedLines: finalized})
	}

	for {
		select {
		case <-ctx.Done():
			_ = l.stream.Stop()
			poll(moonshine.FlagForceUpdate)
			summary := summarize(l.finalized)
			l.send(Update{Elapsed: time.Since(start), TTFT: ttft, Done: true, Summary: summary})
			return
		case chunk, ok := <-l.source.Chunks():
			if !ok {
				if u, send := sourceClosedUpdate(l.source, time.Since(start)); send {
					l.send(u)
				}
				return
			}
			if err := l.stream.AddAudio(chunk, moonshine.SampleRate); err != nil {
				l.send(Update{Err: err, Elapsed: time.Since(start)})
			}
		case <-ticker.C:
			poll(0)
		}
	}
}

// trackLines updates per-line stability bookkeeping (l.tracked) from the
// latest transcript snapshot and returns a LineTiming for each line that
// finalized (transitioned to IsComplete) on this call. Lines with empty
// text are ignored until they have something to show, so the clock starts
// on first real content rather than on an empty placeholder.
func (l *Live) trackLines(transcript moonshine.Transcript) []LineTiming {
	var newlyFinalized []LineTiming
	for _, line := range transcript.Lines {
		if line.Text == "" {
			continue
		}
		lp, ok := l.tracked[line.ID]
		if !ok {
			lp = &lineProgress{firstSeen: time.Now(), lastText: line.Text}
			l.tracked[line.ID] = lp
		} else if !lp.done && !line.IsComplete {
			lp.pollCount++
			if line.Text != lp.lastText {
				lp.revisions++
			}
		}
		lp.lastText = line.Text

		if line.IsComplete && !lp.done {
			lp.done = true
			stability := 1.0
			if lp.pollCount > 0 {
				stability = 1 - float64(lp.revisions)/float64(lp.pollCount)
			}
			timing := LineTiming{
				ID:             line.ID,
				TimeToFinal:    time.Since(lp.firstSeen),
				PollCount:      lp.pollCount,
				Revisions:      lp.revisions,
				StabilityRatio: stability,
			}
			l.finalized = append(l.finalized, timing)
			newlyFinalized = append(newlyFinalized, timing)
		}
	}
	return newlyFinalized
}

// sourceClosedUpdate decides what to do when an AudioSource's Chunks channel
// closes: per the serveapi.AudioSource contract, a closed channel alone
// can't distinguish a clean end of stream from an abnormal termination (e.g.
// a dropped network connection), so it checks Err(). If Err() is non-nil, it
// returns an error Update to send; otherwise send is false (clean EOF is not
// itself an error worth surfacing -- Run's ctx.Done() path handles normal
// session shutdown separately). Extracted from Run for testability without a
// real *moonshine.Stream.
func sourceClosedUpdate(source serveapi.AudioSource, elapsed time.Duration) (u Update, send bool) {
	if err := source.Err(); err != nil {
		return Update{Err: err, Elapsed: elapsed}, true
	}
	return Update{}, false
}

func (l *Live) send(u Update) {
	select {
	case l.updates <- u:
	default:
		// Best effort: if the consumer is behind, drop an intermediate
		// update rather than block the feed/poll loop. The next tick will
		// carry a superset of the state anyway (lines only grow/update).
	}
}
