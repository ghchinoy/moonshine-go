package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

func ts() string {
	return time.Now().Format("15:04:05.000")
}

type envelope struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

func main() {
	addr := flag.String("addr", "ws://localhost:8765/ws", "moonshine serve WebSocket URL")
	target := flag.String("target", "", "tmux target pane or window (default: active)")
	dryRun := flag.Bool("dry-run", false, "print tmux commands without executing them")
	speakConfirm := flag.Bool("speak-confirm", false, "speak audio feedback via sidecar TTS on run and low confidence")
	minConfidence := flag.Float64("min-confidence", 0.50, "minimum mean confidence score (0.0-1.0) required to type into shell")
	debug := flag.Bool("debug", false, "print debug trace for every line and command match")
	flag.Parse()

	// 1. Verify tmux preflight
	tmux, err := NewTmuxClient(*target, *dryRun)
	if err != nil {
		log.Fatalf("[voice-tmux] tmux preflight failed: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// 2. Connect to moonshine serve WebSocket endpoint
	conn, _, err := websocket.Dial(ctx, *addr, nil)
	if err != nil {
		log.Fatalf("[voice-tmux] connecting to %s: %v\n(is `moonshine serve --transport ws --allow-actions` running?)", *addr, err)
	}
	defer conn.CloseNow() //nolint:errcheck

	conn.SetReadLimit(10 << 20)

	sink := newWSActionSink(conn)

	// 3. Assemble composite handlers: controlHandler runs first, dictationHandler falls back
	ctrlH := &controlHandler{
		tmux:         tmux,
		speakConfirm: *speakConfirm,
		debug:        *debug,
	}
	dictH := &dictationHandler{
		tmux:          tmux,
		minConfidence: float32(*minConfidence),
		speakConfirm:  *speakConfirm,
		debug:         *debug,
	}

	composite := serveapi.NewCompositeHandler(ctrlH, dictH)
	runner := serveapi.NewAgentRunner(composite, sink)

	events := make(chan serveapi.TranscriptEvent, 16)

	// 4. Inbound WebSocket reader goroutine
	go func() {
		defer close(events)
		for {
			var env envelope
			if err := wsjson.Read(ctx, conn, &env); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[voice-tmux] read error: %v", err)
				return
			}

			switch env.Kind {
			case string(serveapi.KindTranscript):
				var te serveapi.TranscriptEvent
				if err := json.Unmarshal(env.Payload, &te); err != nil {
					continue
				}
				select {
				case events <- te:
				case <-ctx.Done():
					return
				}
			case string(serveapi.KindActionResult):
				var ar serveapi.ActionResult
				if err := json.Unmarshal(env.Payload, &ar); err == nil {
					sink.complete(ar)
				}
			}
		}
	}()

	fmt.Println("=== voice-tmux: voice-controlled terminal bridge ===")
	fmt.Printf("Connected to: %s\n", *addr)
	if *target != "" {
		fmt.Printf("Tmux target:  %s\n", *target)
	} else {
		fmt.Printf("Tmux target:  active window/pane\n")
	}
	if *dryRun {
		fmt.Println("Mode:         DRY-RUN (printing commands, not executing)")
	}
	fmt.Println("\nVoice commands:")
	fmt.Println("  [dictation]     Speak anything to type literally into tmux")
	fmt.Println("  'run it'        Send Enter (executes typed command)")
	fmt.Println("  'interrupt'     Send Ctrl-C")
	fmt.Println("  'clear'         Send Ctrl-L (clear screen)")
	fmt.Println("  'new window'    tmux new-window")
	fmt.Println("  'split right'   tmux split-window -h")
	fmt.Println("  'split down'    tmux split-window -v")
	fmt.Println("  'next window'   tmux next-window")
	fmt.Println("  'scroll up/dn'  Enter copy-mode and scroll")
	fmt.Println("  'stop/resume'   Pause / resume sidecar listening")
	fmt.Println("\n(Ctrl-C to stop voice-tmux)")
	fmt.Println()

	runner.Run(ctx, events)
	fmt.Println("\n[voice-tmux] stopped.")
}

type wsActionSink struct {
	conn    *websocket.Conn
	mu      sync.Mutex
	nextID  int64
	pending map[string]chan serveapi.ActionResult
}

func newWSActionSink(conn *websocket.Conn) *wsActionSink {
	return &wsActionSink{
		conn:    conn,
		pending: make(map[string]chan serveapi.ActionResult),
	}
}

func (s *wsActionSink) newID() string {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.mu.Unlock()
	return "voice-tmux-" + strconv.FormatInt(id, 10)
}

func (s *wsActionSink) complete(res serveapi.ActionResult) {
	s.mu.Lock()
	ch, ok := s.pending[res.ID]
	s.mu.Unlock()
	if ok {
		ch <- res
	}
}

func (s *wsActionSink) Dispatch(ctx context.Context, req serveapi.ActionRequest) (serveapi.ActionResult, error) {
	// Filter out internal sentinel actions that shouldn't touch the server
	if req.Verb == "none" || req.Verb == "" {
		return serveapi.ActionResult{OK: true}, nil
	}

	if req.ID == "" {
		req.ID = s.newID()
	}

	resCh := make(chan serveapi.ActionResult, 1)
	s.mu.Lock()
	s.pending[req.ID] = resCh
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, req.ID)
		s.mu.Unlock()
	}()

	if err := wsjson.Write(ctx, s.conn, req); err != nil {
		return serveapi.ActionResult{}, fmt.Errorf("writing action request: %w", err)
	}

	timeout := 5 * time.Second
	if req.Verb == "speak" {
		timeout = 30 * time.Second
	}

	select {
	case res := <-resCh:
		return res, nil
	case <-time.After(timeout):
		return serveapi.ActionResult{ID: req.ID, OK: false, Err: "timeout waiting for action result"}, nil
	case <-ctx.Done():
		return serveapi.ActionResult{}, ctx.Err()
	}
}
