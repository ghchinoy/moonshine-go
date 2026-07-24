# `moonshine serve` — agentic voice sidecar (multi-agent build guide)

This document is the shared contract for building the `moonshine serve`
daemon. It is written so **two coding agents can work in parallel** without
editing the same files or making conflicting assumptions.

- **bd epic:** `moonshine-go-6nb`
- **Tracking:** all work is in bd (see [AGENTS.md](../AGENTS.md)). `bd ready`
  tells you what is unblocked *right now*; this doc explains *why* the graph
  is shaped the way it is and how to stay out of the other agent's way.
- **Scope of this doc:** interaction pattern, file-ownership map, coordination
  points, and the invariants (backpressure, idempotency, barge-in,
  native-free tests) every task must honor.

If anything here conflicts with an explicit instruction from the user or
orchestrator, the explicit instruction wins — then update this doc.

---

## 1. What we're building

A long-running `moonshine serve` command that turns the existing STT + TTS
pipeline into an **agentic voice sidecar**:

- streams **live transcript events** as JSON over IPC (WebSocket + gRPC) to
  subscribers in any language, and
- accepts inbound **actions** so a built-in **Gemini** agent (or an external
  subscriber) can react to finalized utterances — lookups/RAG, display
  push, voice commands, and **speak-back via TTS**.

### Data flow

```
mic → session.Live ──Update──▶ Hub ──event(JSON)──▶ Transport Manager ──▶ subscribers (WS + gRPC)
                                 ▲                          │
              action(JSON) ──────┘◀─────────────────────────┘
                                 │
                          Dispatcher ──▶ Agent (Gemini function-calling, or ExternalAgent)
                                 │            tools: lookup/retrieve, display_card, run_command, speak
                                 ├─ TTS  (Synthesizer → PlayFloat32, mic-mute barge-in guard)
                                 └─ session control (pause/resume/stop)
```

Two layers, deliberately decoupled:

1. **Event/transport layer** (dumb, generic): serialize `session.Update` →
   JSON events; accept inbound action JSON. Transport-agnostic.
2. **Agent/action layer** (smart): decide what to do with finalized text.

---

## 2. Locked design decisions

Do not relitigate these without updating the epic and this doc:

- **Keep the daemon pipeline in `internal/`.** The model wiring, transports,
  Hub, and dispatcher are not for external Go import — the primary
  extension surface is IPC/JSON, and external sidecars in any language talk
  JSON/proto, not Go.

  **Amendment (`moonshine-go-y0k`/`zj2`):** `pkg/serveapi` is a narrow,
  deliberate exception, added after this was first written. It promotes
  only the *agent-extension contract* — interfaces (`AgentHandler`,
  `Retriever`, `LLMClient`, `AudioSource`) and their wire-format shadow
  structs (`Line`, `TranscriptEvent`, `ActionRequest`, ...) — as an
  additional, Go-native Tier-2 on-ramp for programs that want typed access
  to the same contract Tier 0/1 get as raw JSON. It does not promote the
  daemon pipeline itself: `internal/serve` still owns the Hub, Dispatcher,
  and transports, and daemon assembly now lives in the importable
  `internal/serve.Server`/`ServerConfig` (extracted from `cmd/moonshine/serve.go`,
  which used to be the only place that could construct one — see
  `moonshine-go-ied`). Because `Server`/`ServerConfig` are in `internal/serve`,
  today only code inside this module (`github.com/ghchinoy/moonshine-go`)
  can embed the daemon in-process; a true external module still can't, and
  must run as a separate process talking `pkg/serveapi` over WS/gRPC (the
  shape `samples/go-cascade-faq` uses). Promoting a public
  `pkg/serveapi.Server`-style wrapper so external modules can embed too is
  tracked as follow-up, not yet scheduled. See
  `docs/vision/serveapi-design.md` (gitignored) for the full rationale.
- **New code lives in `internal/serve/`** (+ one file `cmd/moonshine/serve.go`).
- **Transports:** define a `Transport` interface first, then implement
  **both** WebSocket and gRPC behind it, selectable/concurrent via
  `--transport ws,grpc`.
- **LLM:** Google **Gemini** via `google.golang.org/genai`, native
  function-calling.
- **Agent actions:** lookups/RAG, display push, voice commands, speak-back
  (TTS), LLM/tool-calling.

---

## 3. Integration points in existing code

Read these before writing anything; they are the seams the daemon plugs into.

- `session.Live` emits `session.Update` on its `Updates()` channel
  (`internal/session/session.go:156`). `serve` is a **4th consumer**
  alongside the TUI / plain / file-tee consumers in
  `cmd/moonshine/live.go:119-135`. Mirror the finalized-line dedup pattern
  in `teeUpdatesToFile` (`cmd/moonshine/live.go:144`).
- An `Update` distinguishes interim vs final via `Line.IsComplete`, carries a
  stable `Line.ID`, and reports lines that finalized on that poll in
  `Update.FinalizedLines` (`internal/session/session.go:33`). **The agent
  triggers on finalized lines only**, never on flickering interim text.
- `session.send` **drops updates under backpressure** by design
  (`internal/session/session.go:252`) — the next poll carries a superset.
  Therefore every subscriber and the agent **must be idempotent on
  `Line.ID` + `IsComplete`** and must never assume it sees every frame.
- TTS: `moonshine.Synthesizer` (`internal/moonshine/tts.go:45`) →
  `audio.PlayFloat32` (`internal/audio/playback.go`).
- Mic: `audio.StartMicCapture` (`internal/audio/mic.go`) — the one
  deliberate cgo dependency; the moonshine bindings themselves are cgo-free
  and must stay that way (see AGENTS.md).
- Command wiring: `rootCmd.AddCommand(...)` in `cmd/moonshine/root.go:66-72`.
  Reuse `live`'s model flags/helpers (`--arch/--language/--providers` +
  diarization/word-timestamp flags).

---

## 4. Package + file ownership map (the anti-collision rule)

**Rule: exactly one bd task owns each file. Two agents never edit the same
file at the same time.** New layout:

```
internal/serve/event/
  event.go        [P1] TranscriptEvent, ActionRequest, ActionResult, DisplayCard, FromUpdate
internal/serve/
  hub.go          [P1] Hub: fan-out session.Update → N subscribers (drop-oldest, finalized-once)
  dispatcher.go   [P2] Dispatcher: route ActionRequest verbs
  tts.go          [P2] Speaker impl (Synthesizer + PlayFloat32) + barge-in mute hook
  transport.go    [P3] Transport interface + Manager (merge Actions, fan-out Publish)
  ws.go           [P4a / Track A] WebSocket transport
  serve.proto     [P4b / Track A] gRPC service + messages
  grpc.go         [P4b / Track A] gRPC transport (+ generated *.pb.go)
  agent.go        [P5a / Track B] AgentHandler interface, ExternalAgent, CompositeHandler, runner
  intent.go       [P5c / Track B] regex/rules fast-path intent matcher
  retriever.go    [P5d / Track B] Retriever interface + Noop/static impls
  gemini.go       [P5b / Track B] GeminiAgent (google.golang.org/genai function-calling)
cmd/moonshine/
  serve.go        [P6 / integration] serveCmd wiring — the convergence point
```

`internal/tui` and `internal/gcsfetch` stay untouched.

---

## 5. Dependency graph & the two tracks

```
                 P1 (event + Hub)         ← START HERE, blocks everything
                 /      |        \
              P2      P5c       P5d
             /  \        \       /
           P3   P5a       \     /
          /  \    \        \   /
       P4a  P4b    \        P5b
   (Track A: WS/gRPC)   (Track B: agents)
          \    \      /   /
           \    \    /   /
            \    \  /   /
             ▼    ▼▼   ▼
                 P6 (serve.go convergence)
                    |
                   P7 (docs)
```

| bd id | task | owns | blocked by |
|-------|------|------|-----------|
| `moonshine-go-6nb.1`  | **P1 [shared]** event model + Hub | `event/event.go`, `hub.go` | — (ready) |
| `moonshine-go-6nb.2`  | **P2 [shared]** Dispatcher + TTS + barge-in | `dispatcher.go`, `tts.go` | P1 |
| `moonshine-go-6nb.3`  | **P3 [shared]** Transport interface | `transport.go` | P1, P2 |
| `moonshine-go-6nb.4`  | **P4a [Track A]** WebSocket transport | `ws.go` | P3 |
| `moonshine-go-6nb.5`  | **P4b [Track A]** gRPC transport | `serve.proto`, `grpc.go` | P3 |
| `moonshine-go-6nb.6`  | **P5a [Track B]** AgentHandler + ExternalAgent | `agent.go` | P1, P2 |
| `moonshine-go-6nb.7`  | **P5c [Track B]** intent matcher | `intent.go` | P1 |
| `moonshine-go-6nb.8`  | **P5d [Track B]** Retriever | `retriever.go` | P1 |
| `moonshine-go-6nb.9`  | **P5b [Track B]** Gemini agent | `gemini.go` | P1, P2, P5a, P5d |
| `moonshine-go-6nb.10` | **P6 [integration]** serveCmd convergence | `cmd/moonshine/serve.go` | P2,P3,P4a,P4b,P5a,P5b,P5c |
| `moonshine-go-6nb.11` | **P7 [docs]** README/AGENTS | docs | P6 |

### Suggested split

- **Agent 1 (plumbing / Track A):** P1 → P2 → P3, then P4a + P4b.
- **Agent 2 (agent / Track B):** waits on P1 (and P2 for P5a/P5b), then
  P5a + P5c + P5d → P5b.
- Whoever finishes their track first takes **P6**, then **P7**.

P1 is the single serialization point: it freezes the shared `event` /
`ActionRequest` types. After P1 lands, the two tracks proceed independently.

---

## 6. The only two coordination points

Everything else is one-file-per-task and collision-free. These two need a
heads-up between agents:

1. **`go.mod` / `go.sum`.** P4a (websocket lib), P4b
   (`google.golang.org/grpc` + protobuf), and P5b
   (`google.golang.org/genai`) each add dependencies. Expect trivial
   merges. Convention: run `go mod tidy` in your own task and commit the
   `go.mod`/`go.sum` delta with that task; if you hit a conflict, re-run
   `go mod tidy` after merging — never hand-edit the require block.
2. **One line in `cmd/moonshine/root.go`** (`rootCmd.AddCommand(serveCmd)`),
   added by **P6 only**. No other task touches `root.go`. Keep it to that
   single line.

---

## 7. Invariants every task must honor

These are acceptance criteria, not suggestions:

- **Backpressure = drop-oldest, never block.** The Hub and each transport
  drop intermediate/interim frames for a slow subscriber rather than
  blocking the feed/poll loop (mirror `session.send`,
  `internal/session/session.go:252`).
- **Finalized-once idempotency.** Even when interim frames are dropped,
  every finalized line (`IsComplete`, keyed by `Line.ID`) must be delivered
  to each subscriber and to the agent **exactly once**. The Hub provides the
  dedup helper; transports/agents use it.
- **Barge-in guard.** While TTS is playing, mic audio must not be fed into
  the stream (otherwise the agent transcribes its own voice). This is
  **mute-based**, not acoustic echo cancellation — document it as such. The
  `Speaker` interface exposes a `Speaking()` gate the session-feed loop
  checks.
- **Native-free tests.** `make test` runs `go test ./...` with **no native
  libs** (no `libmoonshine`, no mic). All Hub / Dispatcher / Transport /
  Agent logic must be unit-testable with fakes:
  - fake `session.Update` source for the Hub,
  - fake `Transport` for the Manager,
  - fake `Speaker` for the Dispatcher,
  - fake `LLMClient` for `GeminiAgent` (no network),
  - fake `Retriever` for the lookup tool.
  Anything that genuinely needs native libs (real TTS playback, real mic)
  goes behind an interface and is exercised only in `make smoke` / manual.
- **Security gating.** `--allow-actions` gates `speak`, session control, and
  `run_command`. `run_command` is **off by default**.

---

## 8. Frozen type/interface contracts (fill in during P1–P3)

These names are the contract between tasks. Once P1/P2/P3 land, do **not**
rename fields without coordinating — both tracks serialize/depend on them.

```go
// internal/serve/event  (P1)
type TranscriptEvent struct {
    Lines            []Line // interim + final: ID, Text, StartTime, Duration, IsComplete, SpeakerLabel, optional Words
    FinalizedLineIDs []uint64
    TTFTms, ElapsedMs, PollLatencyMs int64
    Done bool
    Err  string
}
type ActionRequest struct { ID string; Verb string; Args json.RawMessage } // verbs: speak, display, session.pause, session.resume, session.stop, agent.result
type ActionResult  struct { ID string; OK bool; Err string }
type DisplayCard   struct { Title, Body, Kind string; Data json.RawMessage }
func FromUpdate(u session.Update) TranscriptEvent

// internal/serve  (P2)
type Speaker interface { Speak(ctx context.Context, text string, opts ...moonshine.Option) error; Speaking() bool }
type Publisher interface { Publish(event any) error } // Hub satisfies this
type Dispatcher struct { /* Speaker, Publisher, SessionControl injected */ }

// internal/serve  (P3)
type Transport interface {
    Start(ctx context.Context) error
    Publish(event any) error
    Actions() <-chan event.ActionRequest
    Close() error
}
// Manager runs N transports, merges Actions(), fans out Publish.

// internal/serve  (Track B)
type AgentHandler interface { OnFinalizedLine(ctx context.Context, line moonshine.Line) []event.ActionRequest } // P5a
type Retriever    interface { Retrieve(ctx context.Context, query string) ([]Result, error) }                  // P5d
type LLMClient    interface { GenerateWithTools(ctx context.Context, history []Turn, tools []Tool) (text string, calls []ToolCall, err error) } // P5b — fake in tests
```

`CompositeHandler` (in `agent.go`, P5a) runs the fast-path `IntentMatcher`
(P5c) first, then falls through to `GeminiAgent` (P5b) on no match.

---

## 9. Per-agent working protocol

1. `bd ready --json` → pick an unblocked task in your track.
2. `bd update <id> --claim` (atomic claim; avoids double-work).
3. Implement **only the files your task owns** (§4). If you need a type from
   another task that isn't landed yet, it's a real blocker — check the graph;
   don't stub the other task's file.
4. Write native-free tests (§7). Run `make test` (and `make build`).
5. Discovered new work? `bd create ... --deps discovered-from:<your-id>`.
   Don't silently expand scope.
6. `bd close <id> --reason "..."`.
7. **Do not commit/push** unless the active profile or the user explicitly
   authorizes it (conservative default — see AGENTS.md "Agent Context
   Profiles"). Report changed files + validation at handoff.

### Verify commands

```bash
make build      # go build -> bin/moonshine
make test       # go test ./... (MUST stay native-free)
make smoke      # real libmoonshine; manual/native only
bd ready --json # what's unblocked for you now
bd dep tree moonshine-go-6nb   # see the whole plan
```
