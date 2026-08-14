// Command domain-customization is a Tier 1/2 external agent for `moonshine serve`,
// built on github.com/ghchinoy/moonshine-go/pkg/serveapi and
// github.com/ghchinoy/moonshine-go/pkg/agentflow.
//
// It demonstrates Moonshine v0.1.2's runtime domain customization (keyterm
// biasing and context passage extraction):
//   - Voice triggers switch keyterm sets on the live transcriber in real time
//     via session.set_keyterms ActionRequests ("switch to cloud", "switch to
//     medical", "switch to finance", "clear domain").
//   - Voice triggers send free-form context text via session.set_context
//     ActionRequests ("load context passage").
//   - AgentFlow global handlers intercept "stop listening" / "resume listening"
//     using d.PauseListening() / d.ResumeListening().
//
// Usage:
//
//	moonshine serve --transport ws --allow-actions --agent external
//	go run . -addr ws://localhost:8765/ws [-debug]
//
// Then say "switch to cloud", "switch to medical", "switch to finance",
// "load context passage", or "clear domain".
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

	"github.com/ghchinoy/moonshine-go/pkg/agentflow"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

var debug bool

func ts() string {
	return time.Now().Format("15:04:05.000")
}

type envelope struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

func main() {
	addr := flag.String("addr", "ws://localhost:8765/ws", "moonshine serve WebSocket URL")
	flag.BoolVar(&debug, "debug", false, "print matching trace and latency stats")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	conn, _, err := websocket.Dial(ctx, *addr, nil)
	if err != nil {
		log.Fatalf("connecting to %s: %v (is `moonshine serve --transport ws --allow-actions` running?)", *addr, err)
	}
	defer conn.CloseNow() //nolint:errcheck

	conn.SetReadLimit(10 << 20) // 10 MiB

	sink := newWSActionSink(conn)

	var ttftLogged bool

	agentHandler := newDomainAgentFlow(sink)
	runner := serveapi.NewAgentRunner(agentHandler, sink)

	events := make(chan serveapi.TranscriptEvent, 16)

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
				if !ttftLogged && ev.TTFTms > 0 {
					ttftLogged = true
					fmt.Printf("[%s] [stats] time to first token: %dms\n", ts(), ev.TTFTms)
				}
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
			}
		}
	}()

	fmt.Printf("connected to %s -- voice triggers:\n"+
		"  - \"switch to cloud\" / \"cloud domain\"\n"+
		"  - \"switch to medical\" / \"clinical domain\"\n"+
		"  - \"switch to finance\" / \"financial domain\"\n"+
		"  - \"load context passage\"\n"+
		"  - \"clear domain\"\n"+
		"  - \"stop listening\" / \"resume listening\"\n"+
		"(Ctrl-C to quit)\n\n", *addr)

	runner.Run(ctx, events)
	fmt.Println("\nstopped.")
}

func newDomainAgentFlow(sink serveapi.ActionSink) serveapi.AgentHandler {
	flow := agentflow.New()
	flow.ActionSink(sink)

	flow.SpeakWith(func(text string) error {
		args, _ := json.Marshal(serveapi.SpeakArgs{Text: text})
		_, err := flow.EmitAction(serveapi.ActionRequest{Verb: "speak", Args: args})
		return err
	})

	// Global control commands
	flow.Always("stop listening", func(d *agentflow.Dialog) error {
		if debug {
			fmt.Printf("[%s] [debug] control: matched \"stop listening\"\n", ts())
		}
		fmt.Printf("[%s] [agent] heard \"stop listening\" -- pausing session\n", ts())
		_, err := d.PauseListening()
		return err
	})

	flow.Always("resume listening", func(d *agentflow.Dialog) error {
		if debug {
			fmt.Printf("[%s] [debug] control: matched \"resume listening\"\n", ts())
		}
		fmt.Printf("[%s] [agent] heard \"resume listening\" -- resuming session\n", ts())
		_, err := d.ResumeListening()
		return err
	})

	// Domain Customization Triggers

	// 1. Cloud / DevOps Vocabulary
	cloudTerms := []string{"Kubernetes", "Ceph", "etcd", "Istio", "Kustomize", "Prometheus", "Grafana"}
	setCloudDomain := func(d *agentflow.Dialog) error {
		fmt.Printf("[%s] [agent] switching to Cloud domain keyterms: %v\n", ts(), cloudTerms)
		args, _ := json.Marshal(serveapi.SetKeytermsArgs{Keyterms: cloudTerms})
		res, err := d.EmitAction(serveapi.ActionRequest{Verb: "session.set_keyterms", Args: args})
		if err != nil || !res.OK {
			fmt.Printf("[%s] [agent] session.set_keyterms failed: %v\n", ts(), err)
			return err
		}
		return d.Say("Loaded cloud infrastructure vocabulary.")
	}
	flow.ListenFor("switch to cloud", setCloudDomain)
	flow.ListenFor("cloud domain", setCloudDomain)

	// 2. Clinical / Healthcare Vocabulary
	medicalTerms := []string{"Atorvastatin", "Lisinopril", "Echocardiogram", "Hydrochlorothiazide", "Metformin", "Amlodipine"}
	setMedicalDomain := func(d *agentflow.Dialog) error {
		fmt.Printf("[%s] [agent] switching to Clinical domain keyterms: %v\n", ts(), medicalTerms)
		args, _ := json.Marshal(serveapi.SetKeytermsArgs{Keyterms: medicalTerms})
		res, err := d.EmitAction(serveapi.ActionRequest{Verb: "session.set_keyterms", Args: args})
		if err != nil || !res.OK {
			fmt.Printf("[%s] [agent] session.set_keyterms failed: %v\n", ts(), err)
			return err
		}
		return d.Say("Loaded clinical vocabulary.")
	}
	flow.ListenFor("switch to medical", setMedicalDomain)
	flow.ListenFor("clinical domain", setMedicalDomain)

	// 3. Financial Vocabulary
	financialTerms := []string{"NASDAQ", "S&P 500", "EBITDA", "Amortization", "Liquidity", "Derivative"}
	setFinancialDomain := func(d *agentflow.Dialog) error {
		fmt.Printf("[%s] [agent] switching to Financial domain keyterms: %v\n", ts(), financialTerms)
		args, _ := json.Marshal(serveapi.SetKeytermsArgs{Keyterms: financialTerms})
		res, err := d.EmitAction(serveapi.ActionRequest{Verb: "session.set_keyterms", Args: args})
		if err != nil || !res.OK {
			fmt.Printf("[%s] [agent] session.set_keyterms failed: %v\n", ts(), err)
			return err
		}
		return d.Say("Loaded financial vocabulary.")
	}
	flow.ListenFor("switch to finance", setFinancialDomain)
	flow.ListenFor("financial domain", setFinancialDomain)

	// 4. Context Passage Biasing
	samplePassage := "Migration notes for the platform team. We will move the remaining services onto Kubernetes this quarter, with Ceph behind the storage classes and etcd holding the cluster state."
	setPassageContext := func(d *agentflow.Dialog) error {
		fmt.Printf("[%s] [agent] submitting context passage for automatic term extraction\n", ts())
		args, _ := json.Marshal(serveapi.SetContextArgs{Context: samplePassage, MaxTerms: 200})
		res, err := d.EmitAction(serveapi.ActionRequest{Verb: "session.set_context", Args: args})
		if err != nil || !res.OK {
			fmt.Printf("[%s] [agent] session.set_context failed: %v\n", ts(), err)
			return err
		}
		return d.Say("Extracted vocabulary from document context passage.")
	}
	flow.ListenFor("load context passage", setPassageContext)
	flow.ListenFor("load context", setPassageContext)

	// 5. Clear Domain Customization
	clearDomain := func(d *agentflow.Dialog) error {
		fmt.Printf("[%s] [agent] clearing domain keyterms\n", ts())
		args, _ := json.Marshal(serveapi.SetKeytermsArgs{Keyterms: []string{}})
		res, err := d.EmitAction(serveapi.ActionRequest{Verb: "session.set_keyterms", Args: args})
		if err != nil || !res.OK {
			fmt.Printf("[%s] [agent] session.set_keyterms clear failed: %v\n", ts(), err)
			return err
		}
		return d.Say("Cleared domain vocabulary.")
	}
	flow.ListenFor("clear domain", clearDomain)
	flow.ListenFor("reset vocabulary", clearDomain)

	flow.Otherwise(func(utterance string) {
		fmt.Printf("[%s] [agent] no match -- try: switch to cloud, switch to medical, switch to finance, load context, or clear domain\n", ts())
	})

	return agentflow.NewHandlerAdapter(flow)
}

type wsActionSink struct {
	conn *websocket.Conn

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan serveapi.ActionResult
}

func newWSActionSink(conn *websocket.Conn) *wsActionSink {
	return &wsActionSink{conn: conn, pending: make(map[string]chan serveapi.ActionResult)}
}

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
		fmt.Printf("[%s] [agent] %s send failed: %v\n", ts(), req.Verb, err)
		return serveapi.ActionResult{}, fmt.Errorf("sending %s action: %w", req.Verb, err)
	}

	select {
	case res := <-ch:
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
	case <-time.After(30 * time.Second):
		fmt.Printf("[%s] [agent] %s timed out waiting for action_result\n", ts(), req.Verb)
		return serveapi.ActionResult{ID: req.ID, OK: false, Err: "timeout waiting for action_result"}, nil
	}
}

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
	return "domain-customization-" + strconv.FormatInt(id, 10)
}
