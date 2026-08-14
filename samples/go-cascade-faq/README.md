# samples/go-cascade-faq — an offline voice FAQ agent, built on `pkg/serveapi` and `pkg/agentflow`

A small external agent that answers spoken questions about moonshine-go's
own [mission](../../docs/MISSION.md) — by listening to `moonshine serve`'s
live transcript, looking up an answer, and speaking it back — entirely
offline, with no LLM API key and no network call beyond the local
WebSocket connection to the sidecar it's already talking to.

This is the flagship Tier 1/2 sample: it's the first real external consumer
of `github.com/ghchinoy/moonshine-go/pkg/serveapi` and `github.com/ghchinoy/moonshine-go/pkg/agentflow`, built the way an actual
third-party Go project would build it (see `go.mod`'s `replace` directive —
this module depends on `moonshine-go` the same way it would once that
module is published, pointed at the local checkout for development).

## What it demonstrates

Each of the four pillars from [docs/MISSION.md](../../docs/MISSION.md), in
one small, runnable program:

- **Composability** — this entire agent lives in its own Go module,
  depending on moonshine-go through `pkg/serveapi` and `pkg/agentflow` (plus a WebSocket
  client to reach `moonshine serve`) — the transcript really is a bus any process can
  attach to.
- **Control** — AgentFlow global handlers (`flow.Always`) intercept "stop
  listening" / "resume listening" *before* any FAQ flow is evaluated,
  sending `session.pause`/`session.resume` actions back to the sidecar over WS.
- **Observability** — every finalized line, trigger match, and action this agent
  takes is printed to stdout as it happens, timestamped, with real numbers: the
  STT engine's own per-line latency (`Line.LastLatencyMs`), the session's time-to-first-token
  (`TranscriptEvent.TTFTms`, logged once), and each action's actual round-trip time
  (`ActionRequest` sent → matching `ActionResult` received). Unmatched utterances
  trigger `flow.Otherwise` (`[agent] no match -- try: ...`) instead of remaining silent.
  Pass `-debug` for per-keyword matching traces plus per-poll latency
  (`TranscriptEvent.PollLatencyMs`).
- **Privacy** — answers come from a fixed, in-process
  `serveapi.StaticRetriever`. Nothing about what you say leaves this
  process except the `speak`/`session.*` actions it deliberately sends back
  to the sidecar it's already connected to.

## Sample Rating

| Metric | Value |
|---|---|
| **Tier** | Tier 1 / Tier 2 (Go extension) |
| **Complexity** | 2/5 |
| **Setup Cost** | Low (offline, local `moonshine serve`) |
| **Demonstrated Pillars** | Composability, Control, Observability, Privacy |
| **Primary Vertical / Use Case** | Offline Voice FAQ & Local Cascade Agent |
| **Appeal** | 5/5 |

## Architecture

```
mic → moonshine serve → WebSocket (TranscriptEvent JSON) → this program
                                                                  │
                                             serveapi.AgentRunner
                                                                  │
                                              agentflow.HandlerAdapter
                                                                  │
                                              agentflow.AgentFlow
                                              ├── flow.Always ("stop/resume listening") ──> session.pause/resume
                                              ├── flow.ListenFor ("mission", "privacy"...) ──> StaticRetriever -> d.Say
                                              └── flow.Otherwise ──> log guidance
                                                                  │
                                                           ActionRequest
                                                                  │
                                                    WebSocket (back to sidecar)
                                                                  │
                                                         Dispatcher → TTS speak-back
```

## Run it

Build/fetch `libmoonshine` first if you haven't (see the repo root
[README](../../README.md)). Then, in one terminal:

```sh
cd ../..  # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
./bin/moonshine serve --transport ws --allow-actions --agent external
```

`--allow-actions` is required — without it the sidecar rejects `speak` and
`session.*` actions (see `docs/serve-sidecar.md`'s security gating).
`--agent external` (the default) tells the daemon *not* to run its own
built-in Gemini agent, since this sample brings its own.

In another terminal:

```sh
cd samples/go-cascade-faq
go run . -addr ws://localhost:8765/ws
```

Then say something containing one of: **mission**, **cascade**,
**privacy**, **control**, **observability**, **composability** — or say
**"stop listening"** / **"resume listening"**.

Pass `-debug` for the underlying per-rule/per-keyword matching trace and
per-poll latency:

```sh
go run . -addr ws://localhost:8765/ws -debug
```

## A note on the demo dataset & AgentFlow phrase matching

The FAQ answers are six short entries pulled straight from `docs/MISSION.md`, wired up via `flow.ListenFor` in `newAgentFlow()` in `main.go`.

`pkg/agentflow` uses `PhraseMatcher` to evaluate utterances against trigger phrases (see the official interactive [AgentFlow explainer](https://moonshine.ai/agent-flow/)). Without a native embedding model loaded (see open task `moonshine-go-tpg`), `PhraseMatcher` operates on case-insensitive substring matching. Once native `EmbeddingModel` C API bindings land, `pkg/agentflow` automatically gains cosine-similarity vector matching without any code changes in this sample.

## Alternative pattern: deterministic regex fast-paths (`IntentMatcher`)

For simple, single-turn voice control commands without multi-turn conversation flows (`Say`/`Ask`/`Confirm`), `moonshine serve`'s built-in fast path uses `internal/serve.IntentMatcher` (a 95-line deterministic regex rule engine returning synchronous control-plane action requests).

Here is how the session pause/resume controls in this sample compare when written as a regex rule matcher versus `pkg/agentflow`:

```go
// Deterministic regex fast-path (internal/serve.IntentMatcher style)
type controlHandler struct{}

func (c *controlHandler) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
    switch {
    case stopListeningRe.MatchString(line.Text):
        return []serveapi.ActionRequest{{Verb: "session.pause"}}
    case resumeListeningRe.MatchString(line.Text):
        return []serveapi.ActionRequest{{Verb: "session.resume"}}
    default:
        return nil
    }
}

// AgentFlow DSL (pkg/agentflow style -- used in this sample)
flow := agentflow.New()
flow.ActionSink(sink)
flow.Always("stop listening", func(d *agentflow.Dialog) error {
    _, err := d.PauseListening()
    return err
})
flow.Always("resume listening", func(d *agentflow.Dialog) error {
    _, err := d.ResumeListening()
    return err
})
```

Both patterns are valid: use `IntentMatcher`-style regex for zero-dependency, single-shot control rules; use `pkg/agentflow` when building structured, multi-turn voice agents (`Say`, `Ask`, `Confirm`, `Choose`).
