// Command voice-mcp-assistant is a Tier 2 external Go voice agent for
// `moonshine serve` that connects live speech-to-text transcripts to Google
// Gemini (gemini-3.5-flash-lite default, gemini-3.8-flash, or gemini-4-argon)
// and dispatches function calls to external Model Context Protocol (MCP)
// servers over stdio (mcp.CommandTransport).
//
// It is the inverse of samples/mcp-transcribe: whereas mcp-transcribe exposes
// Moonshine as an MCP tool server for desktop LLM hosts, voice-mcp-assistant
// uses Moonshine as the live voice front-end and acts as an MCP client to
// inspect files, directories, and git state hands-free.
//
// Usage:
//
//	# Terminal 1: Start moonshine serve in external agent mode
//	./bin/moonshine serve --transport ws --allow-actions --agent external
//
//	# Terminal 2: Run voice-mcp-assistant
//	cd samples/voice-mcp-assistant
//	export GEMINI_API_KEY="your-api-key"
//	go run . -addr ws://localhost:8765/ws -root ../..
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

func ts() string {
	return time.Now().Format("15:04:05.000")
}

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, "; ")
}

func (m *multiFlag) Set(val string) error {
	*m = append(*m, val)
	return nil
}

type envelope struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

func main() {
	var (
		addr            = flag.String("addr", "ws://localhost:8765/ws", "moonshine serve WebSocket URL")
		model           = flag.String("model", DefaultGeminiModel, "Gemini model ID (gemini-3.5-flash-lite, gemini-3.8-flash, or gemini-4-argon; never gemini-2.*)")
		rootDir         = flag.String("root", ".", "Workspace root directory for sandboxed MCP tools")
		allowWrite      = flag.Bool("allow-write", false, "Allow mutating MCP tools (default: false / read-only)")
		maxRounds       = flag.Int("max-rounds", 4, "Maximum MCP tool-calling rounds per spoken utterance")
		debug           = flag.Bool("debug", false, "Log detailed MCP tool payloads and poll latencies")
		serveBuiltinMCP = flag.Bool("serve-builtin-mcp", false, "Internal: run the built-in stdio MCP workspace server and exit")
		runBenchmark    = flag.Bool("benchmark", false, "Run empirical latency & tool-accuracy comparison across gemini-3.5-flash-lite, gemini-3.8-flash, and gemini-4-argon")
		mcpCmds         multiFlag
	)
	flag.Var(&mcpCmds, "mcp-cmd", "External stdio MCP server command to launch (repeatable; defaults to built-in workspace MCP subprocess)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Mode 1: Subprocess stdio MCP server mode (launched via mcp.CommandTransport)
	if *serveBuiltinMCP {
		srv, err := buildWorkspaceMCPServer(*rootDir, *allowWrite)
		if err != nil {
			log.Fatalf("initializing workspace MCP server: %v", err)
		}
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			log.Fatalf("workspace MCP server error: %v", err)
		}
		return
	}

	if err := ValidateGeminiModel(*model); err != nil {
		log.Fatalf("invalid -model: %v", err)
	}

	// Initialize MCP client bridge and connect stdio MCP server subprocess(es)
	bridge := NewMCPBridge(*allowWrite, *debug)
	defer bridge.Close()

	if len(mcpCmds) == 0 {
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("resolving executable path: %v", err)
		}
		subArgs := []string{"-serve-builtin-mcp", "-root", *rootDir}
		if *allowWrite {
			subArgs = append(subArgs, "-allow-write")
		}
		if err := bridge.ConnectCommand(ctx, "workspace", exe, subArgs...); err != nil {
			log.Fatalf("connecting built-in stdio MCP server: %v", err)
		}
	} else {
		for i, rawCmd := range mcpCmds {
			fields := strings.Fields(rawCmd)
			if len(fields) == 0 {
				continue
			}
			srvName := fmt.Sprintf("mcp-%d", i+1)
			if err := bridge.ConnectCommand(ctx, srvName, fields[0], fields[1:]...); err != nil {
				log.Fatalf("connecting MCP server %q: %v", rawCmd, err)
			}
		}
	}

	// Mode 2: Empirical model comparison benchmark spike (-benchmark)
	if *runBenchmark {
		if err := executeModelBenchmark(ctx, bridge, *maxRounds, *debug); err != nil {
			log.Fatalf("benchmark failed: %v", err)
		}
		return
	}

	// Mode 3: Live WebSocket voice agent connected to moonshine serve
	llmClient, err := NewGeminiMCPClient(ctx, *model, "", bridge)
	if err != nil {
		log.Fatalf("initializing Gemini client: %v", err)
	}

	conn, _, err := websocket.Dial(ctx, *addr, nil)
	if err != nil {
		log.Fatalf("connecting to %s: %v (is `moonshine serve --transport ws --allow-actions --agent external` running?)", *addr, err)
	}
	defer conn.CloseNow() //nolint:errcheck
	conn.SetReadLimit(10 << 20)

	sink := newWSActionSink(conn)
	agent := NewVoiceMCPAgent(llmClient, bridge, *maxRounds, *debug)
	runner := serveapi.NewAgentRunner(agent, sink)

	// Keyboard unpause listener on terminal stdin: dispatches session.resume to sidecar
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			fmt.Printf("[%s] [control] keyboard unpause: sending session.resume\n", ts())
			_, _ = sink.Dispatch(ctx, serveapi.ActionRequest{Verb: "session.resume"})
		}
	}()

	events := make(chan serveapi.TranscriptEvent, 16)
	var ttftLogged bool

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
					continue
				}
				for _, l := range ev.FinalizedLines() {
					fmt.Printf("[%s] [you said] %s  (stt: %dms)\n", ts(), l.Text, l.LastLatencyMs)
				}
				if !ttftLogged && ev.TTFTms > 0 {
					ttftLogged = true
					fmt.Printf("[%s] [stats] time to first token: %dms\n", ts(), ev.TTFTms)
				}
				select {
				case events <- ev:
				case <-ctx.Done():
					return
				}
			case string(serveapi.KindActionResult):
				var res serveapi.ActionResult
				if err := json.Unmarshal(env.Payload, &res); err == nil {
					sink.complete(res)
				}
			case string(serveapi.KindDisplay):
				if *debug {
					var card serveapi.DisplayCard
					if err := json.Unmarshal(env.Payload, &card); err == nil {
						fmt.Printf("[%s] [display] [%s] %s\n", ts(), card.Kind, card.Title)
					}
				}
			}
		}
	}()

	fmt.Println("=== voice-mcp-assistant: Tier 2 Gemini + MCP Voice Agent ===")
	fmt.Printf("Connected to: %s\n", *addr)
	fmt.Printf("Gemini model: %s\n", *model)
	fmt.Printf("Workspace:    %s (allow-write: %v)\n", *rootDir, *allowWrite)
	fmt.Printf("MCP tools:    %s\n", strings.Join(bridge.ToolNames(), ", "))
	fmt.Println("\nTry asking aloud:")
	fmt.Println("  - \"What files or folders are in the workspace?\"")
	fmt.Println("  - \"What is the current git branch and status?\"")
	fmt.Println("  - \"What were the last three git commits?\"")
	fmt.Println("  - \"Read the top of README.md and summarize it.\"")
	fmt.Println("  - \"Stop listening\" / \"Start listening\" (or press Enter to resume)")
	fmt.Println("(Ctrl-C to quit)")
	fmt.Println()

	runner.Run(ctx, events)
	fmt.Println("\nstopped.")
}

// --- Benchmark mode for moonshine-go-ybvs ---

type benchPrompt struct {
	query        string
	expectedTool string
}

var defaultBenchPrompts = []benchPrompt{
	{"What files and folders are in the top level of the workspace?", "list_directory"},
	{"What is the current git branch and working tree status?", "git_status"},
	{"What were the last 3 git commits in this repository?", "git_log"},
	{"List the subdirectories inside the samples directory.", "list_directory"},
	{"Read the first 25 lines of AGENTS.md and tell me the two agent personae.", "read_file"},
	{"Check git status and tell me if there are any untracked files.", "git_status"},
}

func executeModelBenchmark(ctx context.Context, bridge *MCPBridge, maxRounds int, debug bool) error {
	models := []string{
		"gemini-3.5-flash-lite",
		"gemini-3.8-flash",
		"gemini-4-argon",
	}

	type modelSummary struct {
		model        string
		prompts      int
		toolMatches  int
		firstLLMMs   []float64
		totalLLMMs   []float64
		mcpToolMs    []float64
		totalTurnMs  []float64
		avgWordsResp float64
	}

	var summaries []modelSummary

	fmt.Printf("=== Running Voice MCP Tool-Calling Benchmark (%d prompts x %d models) ===\n", len(defaultBenchPrompts), len(models))
	fmt.Printf("Active MCP Tools: %s\n\n", strings.Join(bridge.ToolNames(), ", "))

	for _, m := range models {
		fmt.Printf("--- Benchmarking Model: %s ---\n", m)
		client, err := NewGeminiMCPClient(ctx, m, "", bridge)
		if err != nil {
			return err
		}
		agent := NewVoiceMCPAgent(client, bridge, maxRounds, debug)

		sum := modelSummary{
			model:   m,
			prompts: len(defaultBenchPrompts),
		}
		totalWords := 0

		for i, bp := range defaultBenchPrompts {
			agent.ResetHistory()
			fmt.Printf("\n[%s] Prompt %d/%d: %q (expect tool: %s)\n", m, i+1, len(defaultBenchPrompts), bp.query, bp.expectedTool)
			_ = agent.OnFinalizedLine(ctx, serveapi.Line{ID: uint64(i + 1), Text: bp.query, IsComplete: true})
			metrics := agent.LastMetrics()

			matched := false
			for _, tc := range metrics.ToolCalls {
				if tc == bp.expectedTool {
					matched = true
					break
				}
			}
			if matched {
				sum.toolMatches++
			}
			sum.firstLLMMs = append(sum.firstLLMMs, metrics.FirstLLMMs)
			sum.totalLLMMs = append(sum.totalLLMMs, metrics.TotalLLMMs)
			sum.mcpToolMs = append(sum.mcpToolMs, metrics.TotalMCPToolMs)
			sum.totalTurnMs = append(sum.totalTurnMs, metrics.TotalTurnMs)
			totalWords += len(strings.Fields(metrics.Answer))
		}
		if sum.prompts > 0 {
			sum.avgWordsResp = float64(totalWords) / float64(sum.prompts)
		}
		summaries = append(summaries, sum)
		fmt.Println()
	}

	fmt.Println("==========================================================================================")
	fmt.Println("                     EMPIRICAL VOICE MCP MODEL COMPARISON SUMMARY                         ")
	fmt.Println("==========================================================================================")
	fmt.Println("| Model | Tool Accuracy | First-Call LLM p50 (mean) | Total LLM p50 (mean) | MCP Tool Mean | End-to-End Turn p50 (mean) | Avg Spoken Words |")
	fmt.Println("|---|---|---|---|---|---|---|")
	for _, s := range summaries {
		fmt.Printf("| `%s` | %d/%d (%.0f%%) | %.0fms (%.0fms) | %.0fms (%.0fms) | %.1fms | %.0fms (%.0fms) | %.1f words |\n",
			s.model,
			s.toolMatches, s.prompts, float64(s.toolMatches)/float64(s.prompts)*100,
			percentile(s.firstLLMMs, 0.50), mean(s.firstLLMMs),
			percentile(s.totalLLMMs, 0.50), mean(s.totalLLMMs),
			mean(s.mcpToolMs),
			percentile(s.totalTurnMs, 0.50), mean(s.totalTurnMs),
			s.avgWordsResp,
		)
	}
	fmt.Println("==========================================================================================")
	return nil
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := append([]float64(nil), vals...)
	sort.Float64s(cp)
	idx := int(float64(len(cp)-1) * p)
	return cp[idx]
}

// --- wsActionSink: correlates outbound ActionRequests with ActionResults ---

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
	return "voice-mcp-" + strconv.FormatInt(id, 10)
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

	start := time.Now()
	if err := wsjson.Write(ctx, s.conn, req); err != nil {
		return serveapi.ActionResult{}, fmt.Errorf("writing action request: %w", err)
	}

	timeout := 5 * time.Second
	if req.Verb == "speak" {
		timeout = 30 * time.Second
	}

	select {
	case res := <-resCh:
		if res.OK {
			fmt.Printf("[%s] [action] %s ok (%s round trip)\n", ts(), req.Verb, time.Since(start).Round(time.Millisecond))
		} else {
			fmt.Printf("[%s] [action] %s failed: %s\n", ts(), req.Verb, res.Err)
		}
		return res, nil
	case <-time.After(timeout):
		return serveapi.ActionResult{ID: req.ID, OK: false, Err: "timeout waiting for action result"}, nil
	case <-ctx.Done():
		return serveapi.ActionResult{}, ctx.Err()
	}
}
