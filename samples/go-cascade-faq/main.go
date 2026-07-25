// Command cascade-faq is a Tier 1 external agent for `moonshine serve`,
// built entirely on the public github.com/ghchinoy/moonshine-go/pkg/serveapi
// package: it dials the sidecar's WebSocket endpoint, runs a
// serveapi.AgentRunner + serveapi.CompositeHandler against live finalized
// transcript lines, and answers voice questions about moonshine-go's own
// mission by speaking back through the sidecar's TTS -- all offline, no LLM
// API key, no network call beyond the local WebSocket connection.
//
// It demonstrates all four pillars from docs/MISSION.md in one small,
// runnable program:
//   - Composability: this whole agent lives in its own Go module (see
//     go.mod's replace directive) and talks to the sidecar only through
//     pkg/serveapi + a WebSocket connection -- the shape any real external
//     Go consumer would use.
//   - Control: a small regex fast-path intercepts "stop/resume listening"
//     before it ever reaches the FAQ retriever, using an
//     event.ActionRequest{Verb: "session.pause"/"session.resume"} sent back
//     over the same connection.
//   - Observability: every finalized line and every action this agent takes
//     is printed to stdout as it happens.
//   - Privacy: the FAQ answers come from a fixed local dataset
//     (serveapi.StaticRetriever) -- nothing about what you say leaves this
//     process except the ActionRequests it chooses to send back to the
//     sidecar it's already connected to.
//
// Every finalized line is logged with a wall-clock timestamp and the STT
// engine's own reported per-line latency (Line.LastLatencyMs off the wire --
// a real server-side number, not a client-side stopwatch); the session's
// time-to-first-token (TranscriptEvent.TTFTms) is logged once, the moment
// it's known. A successful action logs its real round-trip time
// (ActionRequest sent -> matching ActionResult received). Nothing is
// silent: a line that matches nothing says so. Pass -debug for the
// underlying per-rule/per-keyword matching trace plus per-poll latency
// (TranscriptEvent.PollLatencyMs).
//
// Usage:
//
//	moonshine serve --transport ws --allow-actions --agent external
//	go run . -addr ws://localhost:8765/ws [-debug]
//
// Then ask about "the mission", "privacy", "control", "observability", or
// "composability" -- or say "stop listening" / "resume listening".
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// debug gates the per-rule/per-keyword matching trace; set from -debug in
// main(). Package-level rather than threaded through every function
// because it's read-only after startup and only affects logging, not
// control flow.
var debug bool

// ts returns a wall-clock timestamp with millisecond precision, prefixed to
// every log line so the real latency of the cascade is visible, not just
// claimed. Matches the format samples/python-agent/agent.py uses, for
// side-by-side comparison.
func ts() string {
	return time.Now().Format("15:04:05.000")
}

// envelope mirrors the {"kind", "payload"} wire shape moonshine serve's
// WebSocket transport uses (internal/serve/ws.go's wireEnvelope). It isn't
// exported from serveapi -- the envelope is transport plumbing, not part of
// the Go extension contract -- so an external client defines its own copy,
// same as samples/listen does.
type envelope struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

func main() {
	addr := flag.String("addr", "ws://localhost:8765/ws", "moonshine serve WebSocket URL")
	flag.BoolVar(&debug, "debug", false, "print per-rule/per-keyword matching trace")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	conn, _, err := websocket.Dial(ctx, *addr, nil)
	if err != nil {
		log.Fatalf("connecting to %s: %v (is `moonshine serve --transport ws --allow-actions` running?)", *addr, err)
	}
	defer conn.CloseNow() //nolint:errcheck

	// moonshine serve omits raw PCM audio from transcript frames by default
	// (see --include-audio); raised defensively in case this connects to a
	// sidecar started with that flag.
	conn.SetReadLimit(10 << 20) // 10 MiB

	sink := newWSActionSink(conn)

	// ttftLogged latches once the session's time-to-first-token is known,
	// so it's reported exactly once instead of on every event that still
	// carries it (TranscriptEvent.TTFTms holds its value for the whole
	// session once set -- see pkg/serveapi/event.go).
	var ttftLogged bool

	faq := newFAQHandler()
	control := newControlHandler(sink)
	agent := &loggingHandler{inner: serveapi.NewCompositeHandler(control, faq)}
	runner := serveapi.NewAgentRunner(agent, sink)

	events := make(chan serveapi.TranscriptEvent, 16)

	// Reader goroutine: the single reader on this connection (nhooyr
	// permits one concurrent reader + many concurrent writers). It demuxes
	// by envelope.Kind: transcript frames feed the AgentRunner, action_result
	// frames complete the matching sink.Dispatch call, display frames are
	// just logged (this sample has no UI).
	go func() {
		defer close(events)
		for {
			var env envelope
			if err := wsjson.Read(ctx, conn, &env); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("connection closed: %v", err)
				return
			}
			switch env.Kind {
			case string(serveapi.KindTranscript):
				var ev serveapi.TranscriptEvent
				if err := json.Unmarshal(env.Payload, &ev); err != nil {
					log.Printf("decoding transcript payload: %v", err)
					continue
				}
				for _, l := range ev.FinalizedLines() {
					fmt.Printf("[%s] [you said] %s  (stt: %dms)\n", ts(), l.Text, l.LastLatencyMs)
				}
				// TTFTms (time to first token) is set once, the first time
				// the session produces any text, and holds that value for
				// the rest of the session -- log it exactly once, the
				// moment it appears, rather than repeating it on every
				// subsequent event.
				if !ttftLogged && ev.TTFTms > 0 {
					ttftLogged = true
					fmt.Printf("[%s] [stats] time to first token: %dms\n", ts(), ev.TTFTms)
				}
				// PollLatencyMs changes every poll; only worth the noise
				// under -debug.
				if debug && ev.PollLatencyMs > 0 {
					fmt.Printf("[%s] [debug] poll latency: %dms (elapsed: %dms)\n",
						ts(), ev.PollLatencyMs, ev.ElapsedMs)
				}
				select {
				case events <- ev:
				case <-ctx.Done():
					return
				}
			case string(serveapi.KindActionResult):
				var res serveapi.ActionResult
				if err := json.Unmarshal(env.Payload, &res); err != nil {
					continue
				}
				sink.complete(res)
			case string(serveapi.KindDisplay):
				// Not used by this sample; a display-capable client would
				// render it.
			}
		}
	}()

	fmt.Printf("connected to %s -- ask about \"the mission\", \"privacy\", \"control\",\n"+
		"\"observability\", or \"composability\", or say \"stop listening\" / \"resume listening\".\n"+
		"(Ctrl-C to quit)\n\n", *addr)

	runner.Run(ctx, events)
	fmt.Println("\nstopped.")
}

// --- loggingHandler: wraps the real agent chain to report "no match" -----

// loggingHandler wraps another AgentHandler (here, the CompositeHandler
// chain) and logs when a finalized line triggers nothing. Without this,
// silence and "the agent isn't working" look identical from the outside --
// the same gap that made a real STT mis-transcription (or a phrasing that
// just didn't match any rule) indistinguishable from a bug during this
// sample's own development.
type loggingHandler struct {
	inner serveapi.AgentHandler
}

// OnFinalizedLine implements serveapi.AgentHandler.
func (h *loggingHandler) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
	actions := h.inner.OnFinalizedLine(ctx, line)
	if len(actions) == 0 {
		fmt.Printf("[%s] [agent] no match -- try: mission, cascade, privacy, control, "+
			"observability, composability, or \"stop/resume listening\"\n", ts())
	}
	return actions
}

// --- FAQ handler: the "cascade brings back RAG" demo ---------------------

// faqEntry is one keyword-triggered answer, sourced from docs/MISSION.md.
type faqEntry struct {
	keyword string // spotted as a substring of the spoken line (case-insensitive)
	result  serveapi.Result
}

// faqHandler is a serveapi.AgentHandler that spots a small set of keywords
// in finalized lines and, on a hit, asks a serveapi.StaticRetriever for the
// matching entry and speaks its snippet back. The keyword-spotting step
// exists because StaticRetriever.Retrieve matches a query substring against
// Title/Snippet/Source and returns nothing for an empty query -- it's
// designed to be called with an extracted term, not a whole spoken
// sentence.
type faqHandler struct {
	entries   []faqEntry
	retriever *serveapi.StaticRetriever
}

func newFAQHandler() *faqHandler {
	entries := []faqEntry{
		{"mission", serveapi.Result{
			Title:   "Mission",
			Snippet: "moonshine go is bringing back the classic voice cascade: speech to text, to a language model, to speech syntheisis - because streaming transcription is finally fast enough to make it viable again.",
			Source:  "docs/MISSION.md",
		}},
		{"cascade", serveapi.Result{
			Title:   "The cascade",
			Snippet: "The cascade never lost on capability. It lost on milliseconds. And the milliseconds are no longer the problem.",
			Source:  "docs/MISSION.md",
		}},
		{"privacy", serveapi.Result{
			Title:   "Privacy",
			Snippet: "Audio can die at the microphone. Only the text you choose ever needs to leave the box.",
			Source:  "docs/MISSION.md",
		}},
		{"control", serveapi.Result{
			Title:   "Control",
			Snippet: "Every stage of the cascade is yours to gate, swap, and reason about.",
			Source:  "docs/MISSION.md",
		}},
		{"observability", serveapi.Result{
			Title:   "Observability",
			Snippet: "Every utterance is an inspectable event you can log, diff, and replay.",
			Source:  "docs/MISSION.md",
		}},
		{"composability", serveapi.Result{
			Title:   "Composability",
			Snippet: "The transcript is a bus other processes attach to, in any language. This very agent is one of those processes.",
			Source:  "docs/MISSION.md",
		}},
	}

	items := make([]serveapi.Result, len(entries))
	for i, e := range entries {
		items[i] = e.result
	}
	return &faqHandler{entries: entries, retriever: serveapi.NewStaticRetriever(items...)}
}

// OnFinalizedLine implements serveapi.AgentHandler.
func (f *faqHandler) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
	text := strings.ToLower(line.Text)
	for _, e := range f.entries {
		if !strings.Contains(text, e.keyword) {
			if debug {
				fmt.Printf("[%s] [debug] faq: checked %q -> miss\n", ts(), e.keyword)
			}
			continue
		}
		if debug {
			fmt.Printf("[%s] [debug] faq: checked %q -> HIT\n", ts(), e.keyword)
		}
		// Genuinely calls through the public Retriever interface with the
		// spotted keyword, rather than just reading f.entries directly --
		// this is the actual retrieval path a real RAG-backed handler
		// would use, just with a trivial in-memory backend.
		retrieveStart := time.Now()
		results, err := f.retriever.Retrieve(ctx, e.keyword)
		if debug {
			fmt.Printf("[%s] [debug] faq: retriever.Retrieve(%q) -> %d result(s) (%s)\n",
				ts(), e.keyword, len(results), time.Since(retrieveStart))
		}
		if err != nil || len(results) == 0 {
			continue
		}
		fmt.Printf("[%s] [agent] matched %q -- speaking answer\n", ts(), e.keyword)
		args, _ := json.Marshal(serveapi.SpeakArgs{Text: results[0].Snippet})
		return []serveapi.ActionRequest{{Verb: "speak", Args: args}}
	}
	return nil
}

// --- Control handler: fast-path session commands, no LLM required --------

var (
	stopListeningRe   = regexp.MustCompile(`(?i)^\s*(stop|pause)\s+listening\s*\.?$`)
	resumeListeningRe = regexp.MustCompile(`(?i)^\s*(resume|start)\s+listening\s*\.?$`)
)

// controlHandler is a serveapi.AgentHandler demonstrating the "control"
// pillar from a Tier 1 (external) agent: it recognizes a couple of
// deterministic voice commands and sends session-control ActionRequests
// back to the sidecar, ahead of the FAQ handler in the CompositeHandler
// chain. This mirrors internal/serve's own IntentMatcher pattern, just
// implemented independently against the public interfaces -- proof that
// "fast-path regex before falling through to something smarter" isn't an
// internal-only trick.
type controlHandler struct {
	sink serveapi.ActionSink
}

func newControlHandler(sink serveapi.ActionSink) *controlHandler {
	return &controlHandler{sink: sink}
}

// OnFinalizedLine implements serveapi.AgentHandler.
func (c *controlHandler) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
	if debug {
		fmt.Printf("[%s] [debug] control: checked stop_listening -> %s\n", ts(), hitOrMiss(stopListeningRe.MatchString(line.Text)))
		fmt.Printf("[%s] [debug] control: checked resume_listening -> %s\n", ts(), hitOrMiss(resumeListeningRe.MatchString(line.Text)))
	}
	switch {
	case stopListeningRe.MatchString(line.Text):
		fmt.Printf("[%s] [agent] heard \"stop listening\" -- pausing session\n", ts())
		return []serveapi.ActionRequest{{Verb: "session.pause"}}
	case resumeListeningRe.MatchString(line.Text):
		fmt.Printf("[%s] [agent] heard \"resume listening\" -- resuming session\n", ts())
		return []serveapi.ActionRequest{{Verb: "session.resume"}}
	default:
		return nil
	}
}

// hitOrMiss renders a bool as "HIT"/"miss" for debug trace lines.
func hitOrMiss(matched bool) string {
	if matched {
		return "HIT"
	}
	return "miss"
}

// --- wsActionSink: serveapi.ActionSink over the shared WS connection -----

// wsActionSink implements serveapi.ActionSink by writing ActionRequest
// frames to a shared WebSocket connection and correlating the matching
// ActionResult frame (read back by main's reader goroutine and handed to
// complete) by ID, so Dispatch can honor its synchronous
// (ActionResult, error) contract even though the transport is async.
type wsActionSink struct {
	conn *websocket.Conn

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan serveapi.ActionResult
}

func newWSActionSink(conn *websocket.Conn) *wsActionSink {
	return &wsActionSink{conn: conn, pending: make(map[string]chan serveapi.ActionResult)}
}

// Dispatch implements serveapi.ActionSink.
func (s *wsActionSink) Dispatch(ctx context.Context, req serveapi.ActionRequest) (serveapi.ActionResult, error) {
	if req.ID == "" {
		req.ID = s.newID()
	}
	ch := make(chan serveapi.ActionResult, 1)
	s.mu.Lock()
	s.pending[req.ID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, req.ID)
		s.mu.Unlock()
	}()

	if debug {
		fmt.Printf("[%s] [debug] dispatch: sending %s (id=%s)\n", ts(), req.Verb, req.ID)
	}
	sentAt := time.Now()
	if err := wsjson.Write(ctx, s.conn, req); err != nil {
		// AgentRunner.ProcessLine discards Dispatch's returned error, so
		// this is the only place a send failure is visible -- without it,
		// a write error and "the sidecar just isn't responding" look
		// identical from the terminal.
		fmt.Printf("[%s] [agent] %s send failed: %v\n", ts(), req.Verb, err)
		return serveapi.ActionResult{}, fmt.Errorf("sending %s action: %w", req.Verb, err)
	}

	select {
	case res := <-ch:
		// Real round-trip time -- ActionRequest sent to matching
		// ActionResult received, including whatever the sidecar actually
		// did (e.g. full TTS synthesis + playback for "speak") -- not a
		// synthetic client-side timer.
		elapsed := time.Since(sentAt)
		if res.OK {
			fmt.Printf("[%s] [agent] %s ok (%s round trip)\n", ts(), req.Verb, elapsed)
		} else {
			fmt.Printf("[%s] [agent] %s failed: %s (%s round trip)\n", ts(), req.Verb, res.Err, elapsed)
		}
		return res, nil
	case <-ctx.Done():
		fmt.Printf("[%s] [agent] %s canceled: %v (%s since send)\n", ts(), req.Verb, ctx.Err(), time.Since(sentAt))
		return serveapi.ActionResult{}, ctx.Err()
	case <-time.After(dispatchTimeout(req.Verb)):
		// This is a real, previously-silent failure mode caught live
		// against this sample: the sidecar's Speaker.Speak blocks for the
		// full TTS synthesis + local playback duration before it returns
		// (see internal/serve/dispatcher.go's Speaker interface doc
		// comment) -- for this sample's longer FAQ answers that's 15s+
		// end to end, well past a naive fixed 5s timeout. Without a
		// verb-aware timeout and this log line, the terminal shows
		// "matched ... -- speaking answer" and then silence forever,
		// indistinguishable from a hang even though the sidecar was
		// working the whole time.
		timeout := dispatchTimeout(req.Verb)
		fmt.Printf("[%s] [agent] %s timed out waiting for action_result (%s)\n", ts(), req.Verb, timeout)
		return serveapi.ActionResult{ID: req.ID, OK: false, Err: "timeout waiting for action_result"}, nil
	}
}

// dispatchTimeout returns how long Dispatch waits for a matching
// action_result before giving up, per verb. "speak" gets a much longer
// budget because the sidecar's Speak blocks for the full synthesize +
// play-locally duration -- see the timeout case above for how this was
// discovered.
func dispatchTimeout(verb string) time.Duration {
	if verb == "speak" {
		return 30 * time.Second
	}
	return 5 * time.Second
}

// complete delivers a received ActionResult to the goroutine awaiting it in
// Dispatch, if any. Called from main's single reader goroutine.
func (s *wsActionSink) complete(res serveapi.ActionResult) {
	s.mu.Lock()
	ch, ok := s.pending[res.ID]
	s.mu.Unlock()
	if ok {
		ch <- res
	}
}

func (s *wsActionSink) newID() string {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.mu.Unlock()
	return "cascade-faq-" + strconv.FormatInt(id, 10)
}
