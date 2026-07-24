# samples — build your first voice agent against `moonshine serve`

This is the hands-on developer walkthrough for `moonshine serve`, the
agentic voice sidecar (see [../docs/MISSION.md](../docs/MISSION.md) for the
"why"). Every example here is real, runnable code, not prose — if it stops
working, `go build`/`python` says so immediately instead of the breakage
sitting undiscovered in a doc.

`moonshine serve` runs a local STT + TTS pipeline as a daemon that streams
live transcripts over WebSocket or gRPC and executes inbound actions
(`speak`, `display`, `session.pause/resume/stop`, `run_command`).

Want to add a sample? See [CONTRIBUTING.md](CONTRIBUTING.md) for
conventions and the verification bar.

---

## Architecture overview

```
mic → session.Live ──Update──▶ Hub ──event(JSON)──▶ Transports (WS + gRPC) ──▶ Subscribers
                                 ▲                          │
              action(JSON) ──────┘◀─────────────────────────┘
                                 │
                          Dispatcher ──▶ Agent (Gemini or external IPC subscriber)
                                 │            tools: lookup/retrieve, display_card, speak
                                 ├─ TTS  (Synthesizer → PlayFloat32, mic-mute barge-in guard)
                                 └─ session control (pause/resume/stop)
```

Two layers, deliberately decoupled: an **event/transport layer** (dumb,
generic — serialize updates to JSON, accept inbound action JSON, no
knowledge of what any of it means) and an **agent/action layer** (smart —
decides what finalized text should trigger). This is why the sidecar
supports three independent integration tiers, each fully served by the
same layer split.

The "mic" at the top isn't fixed to the local microphone, either: `--audio-source
remote` swaps it for a network PCM source (binary WS frames), which is what
makes [browser-listen](browser-listen/) possible — the daemon and the
microphone don't have to be on the same machine.

## Start the sidecar daemon

```sh
cd .. # repo root, if you're not there already
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"  # see repo README if you haven't built/fetched libmoonshine yet
./bin/moonshine serve --transport ws,grpc --addr :8765 --grpc-addr :9090 --allow-actions --agent external
```

`--allow-actions` gates the mutating verbs (`speak`, `session.*`,
`run_command`); it's required for any Tier 1/2 example that talks back.
`--agent external` (the default) means the daemon runs no logic of its own —
every sample below brings its own agent as a separate process.

---

## Tier 0: subscribe to live transcripts (any language)

Connect to the WebSocket endpoint (`ws://localhost:8765/ws`) and decode the
`{"kind": "transcript", "payload": {...}}` envelope. That's the entire
contract — no SDK, no codegen, just JSON over a socket.

### Invariants to honor

- **Backpressure = drop-oldest.** The sidecar drops intermediate interim
  frames for a slow subscriber. The next frame carries a fresh, complete
  snapshot — you never need an old one.
- **Finalized-once idempotency.** Dedupe on `finalized_line_ids`, not by
  scanning every line's `is_complete` flag every frame (a line finalized on
  an earlier poll still has `is_complete: true` on every later snapshot —
  scanning for it would double-report).

### Examples

- [go-listen](go-listen/) — ~90 lines of Go, zero moonshine-go dependency
  (hand-decodes the wire envelope). Proves the transcript is a real
  cross-process, cross-language bus with the smallest possible surface.
- [python-listen](python-listen/) — the same idea in ~40 lines of Python
  with the `websockets` library.

---

## Tier 1: external agent via action requests

An external process (any language) receives finalized lines over WebSocket
and sends `ActionRequest` JSON back over the same connection to trigger
sidecar-side effects.

### Action request JSON format

```json
{
  "id": "req-001",
  "verb": "speak",
  "args": { "text": "Hello, I am your external voice assistant." }
}
```

### Supported action verbs

| Verb | Args payload | Effect |
|---|---|---|
| `speak` | `{"text": "...", "voice": "...", "speed": 1.0}` | Synthesizes speech and plays it through the default output device |
| `display` | `{"title": "...", "body": "...", "kind": "info"}` | Fans out a `DisplayCard` event to all connected UI subscribers |
| `session.pause` | *(none)* | Mutes mic input and pauses live transcription |
| `session.resume` | *(none)* | Resumes mic input and transcription |
| `session.stop` | *(none)* | Stops the sidecar daemon session |

### Examples

- [python-agent](python-agent/) — a Python agent that recognizes a couple
  of deterministic voice commands and speaks a real answer back, with no
  moonshine-go dependency at all (just `websockets` + `json`).

---

## Tier 2: Go extension via `pkg/serveapi`

For Go programs, [`pkg/serveapi`](../pkg/serveapi) gives you typed,
Go-native versions of the same contract: `AgentHandler`, `Retriever`,
`LLMClient`, `AgentRunner`, `CompositeHandler`, and shadow structs for every
wire type (`TranscriptEvent`, `Line`, `ActionRequest`, ...).

**A note on in-process embedding vs. a separate agent process.** Daemon
assembly (loading the model, opening the mic, wiring
Hub/Dispatcher/Transports) is importable today as `internal/serve.Server` /
`ServerConfig` — but that's `internal/`, so only code *inside this module*
can construct and run the daemon in-process with a custom `AgentHandler`.
An external module (a real third-party consumer, depending on
`github.com/ghchinoy/moonshine-go` from the outside) can't import it, so
"Tier 2" for an external consumer means: write a normal Go program, dial
the sidecar's WebSocket endpoint, and drive `pkg/serveapi`'s interfaces
client-side — the same shape as Tier 1, just with real Go types instead of
hand-rolled JSON structs, and reusable interfaces (`AgentHandler`,
`Retriever`) instead of one-off logic. (A public `pkg/serveapi.Server`
wrapper that would let external modules embed too is tracked as
`moonshine-go-axa`, not yet built.)

The shape looks like this:

```go
type MyFAQHandler struct {
    retriever serveapi.Retriever
}

func (h *MyFAQHandler) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
    results, err := h.retriever.Retrieve(ctx, extractKeyword(line.Text))
    if err != nil || len(results) == 0 {
        return nil
    }
    args, _ := json.Marshal(serveapi.SpeakArgs{Text: results[0].Snippet})
    return []serveapi.ActionRequest{{Verb: "speak", Args: args}}
}
```

Combine a fast-path deterministic matcher with a fallback handler using
`CompositeHandler`, then drive both from an `AgentRunner` fed by frames read
off your own WebSocket connection:

```go
control := &sessionControlHandler{sink: sink}       // regex-based, e.g. "stop listening"
faq := &MyFAQHandler{retriever: serveapi.NewStaticRetriever(myItems...)}
agent := serveapi.NewCompositeHandler(control, faq) // control's fast path runs first
runner := serveapi.NewAgentRunner(agent, sink)      // sink implements serveapi.ActionSink
runner.Run(ctx, events)                             // events: <-chan serveapi.TranscriptEvent, decoded from your WS reads
```

### Examples

- [go-cascade-faq](go-cascade-faq/) — the flagship sample. A complete,
  runnable external agent: `CompositeHandler` with a regex fast-path
  (`session.pause`/`resume`) plus a `StaticRetriever`-backed FAQ over
  `docs/MISSION.md` content, speaking answers back via real TTS. Its own Go
  module (with a `replace` directive to this checkout) so it builds exactly
  the way a real third-party consumer of `pkg/serveapi` would — verified to
  build with `CGO_ENABLED=0`, zero `internal/*` imports.
- [browser-listen](browser-listen/) — a static HTML+JS page:
  `getUserMedia` + `AudioWorklet` captures mic audio in the browser and
  streams it to a remote `moonshine serve` via `--audio-source remote`,
  rendering the live transcript coming back over the same connection. No
  build step, no install of anything — the "composability" pillar taken to
  its logical extreme, and the concrete realization of "browser as the
  audio source" from `docs/hosting.md`.

---

## A note on transcript frame size

By default, `moonshine serve` omits each line's raw PCM audio
(`Line.AudioData`) from transcript frames — audio dies at the microphone
unless you ask for it (see [MISSION.md](../docs/MISSION.md)'s privacy
pillar). Pass `--include-audio` to the daemon if you need raw samples on
the wire (e.g. building a "play back what I said" UI); every example here
works fine with the default.

---

## See also

- [../docs/MISSION.md](../docs/MISSION.md) — why this project exists.
- [../docs/serve-sidecar.md](../docs/serve-sidecar.md) — the sidecar's
  internal architecture and invariants (backpressure, idempotency,
  barge-in) in more depth.
- [../docs/hosting.md](../docs/hosting.md) — running `moonshine serve`
  beyond your own laptop.
- [../docs/user-guide.md](../docs/user-guide.md) — full CLI flag reference.
