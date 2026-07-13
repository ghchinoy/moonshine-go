// Package session orchestrates a live streaming transcription session: it
// owns a moonshine.Stream and a mic audio source, feeds audio in, polls for
// updated transcripts, and reports timing stats (notably time-to-first-token)
// that a CLI or TUI can render.
package session

import (
	"context"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/moonshine"
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
	// Done is true on the final update of the session (after Stop()).
	Done bool
	Err  error
}

// Live runs a live microphone -> streaming transcription session.
type Live struct {
	stream       *moonshine.Stream
	mic          *audio.MicCapture
	pollInterval time.Duration
	updates      chan Update
}

// NewLive creates and starts a streaming session against tr, consuming audio
// from mic. pollInterval controls how often the (already internally rate
// limited) transcribe_stream call is polled; moonshine itself skips
// re-analysis of audio it's seen in the last ~200ms unless forced, so
// anything below that is wasted work.
func NewLive(tr *moonshine.Transcriber, mic *audio.MicCapture, pollInterval time.Duration) (*Live, error) {
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
		mic:          mic,
		pollInterval: pollInterval,
		updates:      make(chan Update, 8),
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
		l.send(Update{Transcript: transcript, TTFT: ttft, Elapsed: time.Since(start), PollLatency: latency})
	}

	for {
		select {
		case <-ctx.Done():
			_ = l.stream.Stop()
			poll(moonshine.FlagForceUpdate)
			l.send(Update{Elapsed: time.Since(start), TTFT: ttft, Done: true})
			return
		case chunk, ok := <-l.mic.Chunks():
			if !ok {
				return
			}
			if err := l.stream.AddAudio(chunk, audio.TargetSampleRate); err != nil {
				l.send(Update{Err: err, Elapsed: time.Since(start)})
			}
		case <-ticker.C:
			poll(0)
		}
	}
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
