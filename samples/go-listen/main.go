// Command listen is the smallest possible consumer of `moonshine serve`'s
// live transcript feed: it dials the WebSocket endpoint, decodes the wire
// envelope by hand (no moonshine-go dependency at all), and prints each
// newly-finalized line as it arrives.
//
// This is deliberately NOT built against pkg/serveapi. The point of this
// sample is that the wire contract itself -- a JSON object per text frame,
// shaped like {"kind": "...", "payload": {...}} -- is the real extension
// surface. Any language with a WebSocket client and a JSON decoder can do
// this; here it's ~50 lines of Go with a single external dependency (a
// WebSocket client library -- Go's stdlib doesn't ship one).
//
// Usage:
//
//	moonshine serve --transport ws --addr :8765
//	go run . -addr ws://localhost:8765/ws
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// envelope mirrors the {"kind", "payload"} shape moonshine serve's
// WebSocket transport sends -- see internal/serve/ws.go's wireEnvelope on
// the server side. We only care about "transcript" frames here.
type envelope struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// transcriptEvent is the subset of event.TranscriptEvent's fields this
// sample needs. We only decode what we use -- the wire format has more
// fields (timing stats, session summary) that a richer client would read.
type transcriptEvent struct {
	Lines            []line   `json:"lines"`
	FinalizedLineIDs []uint64 `json:"finalized_line_ids"`
}

type line struct {
	ID         uint64 `json:"id"`
	Text       string `json:"text"`
	IsComplete bool   `json:"is_complete"`
}

func main() {
	addr := flag.String("addr", "ws://localhost:8765/ws", "moonshine serve WebSocket URL")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	conn, _, err := websocket.Dial(ctx, *addr, nil)
	if err != nil {
		log.Fatalf("connecting to %s: %v (is `moonshine serve --transport ws` running?)", *addr, err)
	}
	defer conn.CloseNow() //nolint:errcheck

	// moonshine serve omits raw PCM audio from transcript frames by default
	// (see --include-audio), so nhooyr's 32KB default read limit is plenty
	// for normal text-only frames. Raised defensively in case the sidecar
	// this connects to was started with --include-audio, which puts each
	// line's raw samples back on the wire.
	conn.SetReadLimit(10 << 20) // 10 MiB

	fmt.Printf("connected to %s -- listening for finalized transcript lines (Ctrl-C to stop)\n\n", *addr)

	seen := make(map[uint64]bool)
	for {
		var env envelope
		if err := wsjson.Read(ctx, conn, &env); err != nil {
			if ctx.Err() != nil {
				fmt.Println("\nstopped.")
				return
			}
			log.Fatalf("reading frame: %v", err)
		}
		if env.Kind != "transcript" {
			continue // display / action_result frames -- not this sample's concern
		}

		var ev transcriptEvent
		if err := json.Unmarshal(env.Payload, &ev); err != nil {
			log.Printf("decoding transcript payload: %v", err)
			continue
		}

		// finalized_line_ids names the lines that newly finalized on THIS
		// event; look them up in Lines for their text. This is the
		// idempotency contract every subscriber must honor (see
		// docs/serve-sidecar.md section 7): interim frames may be dropped
		// under backpressure, but a finalized line ID is only ever new
		// once.
		byID := make(map[uint64]line, len(ev.Lines))
		for _, l := range ev.Lines {
			byID[l.ID] = l
		}
		for _, id := range ev.FinalizedLineIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			if l, ok := byID[id]; ok {
				fmt.Printf("[FINAL] %s\n", l.Text)
			}
		}
	}
}
