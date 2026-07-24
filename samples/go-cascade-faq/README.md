# samples/go-cascade-faq — an offline voice FAQ agent, built on `pkg/serveapi`

A small external agent that answers spoken questions about moonshine-go's
own [mission](../../docs/MISSION.md) — by listening to `moonshine serve`'s
live transcript, looking up an answer, and speaking it back — entirely
offline, with no LLM API key and no network call beyond the local
WebSocket connection to the sidecar it's already talking to.

This is the flagship Tier 1/2 sample: it's the first real external consumer
of `github.com/ghchinoy/moonshine-go/pkg/serveapi`, built the way an actual
third-party Go project would build it (see `go.mod`'s `replace` directive —
this module depends on `moonshine-go` the same way it would once that
module is tagged and published, just pointed at the local checkout for
development).

## What it demonstrates

Each of the four pillars from [docs/MISSION.md](../../docs/MISSION.md), in
one small, runnable program:

- **Composability** — this entire agent lives in its own Go module,
  depending on moonshine-go only through `pkg/serveapi` (plus a WebSocket
  client to reach it) — the transcript really is a bus any process can
  attach to.
- **Control** — a small regex-based `controlHandler` intercepts "stop
  listening" / "resume listening" *before* the FAQ handler ever sees the
  line, sending `session.pause`/`session.resume` actions back to the
  sidecar — the same "fast-path before something smarter" pattern
  `internal/serve`'s own `IntentMatcher` uses, implemented independently
  against the public interfaces.
- **Observability** — every finalized line, every keyword match, and every
  action this agent takes is printed to stdout as it happens.
- **Privacy** — answers come from a fixed, in-process
  `serveapi.StaticRetriever`. Nothing about what you say leaves this
  process except the `speak`/`session.*` actions it deliberately sends back
  to the sidecar it's already connected to.

## Architecture

```
mic → moonshine serve → WebSocket (TranscriptEvent JSON) → this program
                                                                  │
                              serveapi.AgentRunner (dedupes on Line.ID)
                                                                  │
                     serveapi.CompositeHandler(controlHandler, faqHandler)
                                    │                      │
                         session.pause/resume      StaticRetriever lookup
                                    │                      │
                                    └──── ActionRequest ────┘
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

## A note on the demo dataset

The FAQ answers are five short entries pulled straight from
`docs/MISSION.md`, wired up in `newFAQHandler()` in `main.go`. Swapping in
your own knowledge base means implementing `serveapi.Retriever` — the
interface this sample already calls through — with a real backend (a local
file, a vector store, whatever you like); nothing about the agent-runner
wiring above it needs to change.

## Bugs found and fixed while building this sample

Two real defects in `moonshine serve` were found and fixed live against
this sample, not from code review:

- **`moonshine-go-6ba`** (fixed) — every `TranscriptEvent` frame used to
  embed each line's raw PCM audio unconditionally, pushing frame sizes to
  300KB+ for a single short sentence. `moonshine serve` now omits
  `Line.AudioData` by default (opt in with `--include-audio`); this
  sample's `conn.SetReadLimit` call is now just defensive headroom, not a
  required workaround.
- **`moonshine-go-ule`** (fixed) — `moonshine serve`'s TTS speak-back path
  never passed the `g2p_root` option to the synthesizer, so every `speak`
  action failed with `creating tts synthesizer: ... Unknown error (code
  -1)` even in an environment where the standalone `moonshine tts` command
  worked fine. `cmd/moonshine/serve.go` now mirrors `cmd/moonshine/tts.go`'s
  `g2p_root` wiring (and exposes it as a `--g2p-root` flag on `serve` too).
