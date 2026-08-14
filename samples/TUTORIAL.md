# Tutorial: Build an Offline Voice Agent in 60 Minutes

Welcome to the hands-on developer tutorial for **moonshine-go**! This 4-part guided walkthrough takes you step-by-step from subscribing to live transcript feeds to building a complete, offline voice agent with multi-turn conversation flows — all running locally on your machine with zero cloud API keys and sub-100ms fast-path response times.

---

## Prerequisites

1. **Go 1.22+** (or Python 3.9+ if exploring non-Go examples).
2. **Build `moonshine` CLI & native library:**
   ```sh
   make buildlib  # or download prebuilt libmoonshine per root README.md
   make build     # compiles bin/moonshine
   ```

---

## Architecture Overview

`moonshine serve` operates as an agentic voice sidecar. It runs Speech-to-Text (STT) and Text-to-Speech (TTS) locally, exposing a lightweight transport bus (WebSocket & gRPC):

```
mic → moonshine serve → WebSocket (TranscriptEvent JSON) → your agent
                                                                │
                                                        ActionRequest (JSON)
                                                                │
                                WebSocket (back to sidecar) ────┘
                                         │
                               Dispatcher → TTS speak-back / session control
```

---

## Part 1: Subscribe to Live Transcripts (Tier 0)

In Part 1, you connect to `moonshine serve` as a passive subscriber to receive real-time streaming transcripts.

### 1. Start the sidecar daemon
In Terminal 1:
```sh
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
./bin/moonshine serve --transport ws --addr :8765
```

### 2. Connect a transcript listener
In Terminal 2, run either the Go or Python Tier 0 listener:

- **Go ([`samples/go-listen/`](go-listen/)):**
  ```sh
  cd samples/go-listen
  go run . -addr ws://localhost:8765/ws
  ```
- **Python ([`samples/python-listen/`](python-listen/)):**
  ```sh
  cd samples/python-listen
  python3 listen.py --addr ws://localhost:8765/ws
  ```

### Key Concepts

- **Transport Envelope:** Every message arrives as a JSON envelope `{"kind": "transcript", "payload": {...}}`.
- **Finalized-Once Idempotency:** To avoid duplicate lines, track processed line IDs using `ev.FinalizedLineIDs()` rather than inspecting `line.IsComplete`.

---

## Part 2: Speak Back & Execute Control Actions (Tier 1)

In Part 2, your application sends `ActionRequest` JSON frames back over the WebSocket connection to speak text aloud or control the sidecar session (`session.pause`, `session.resume`, `session.stop`).

### 1. Start the sidecar with actions enabled
In Terminal 1:
```sh
./bin/moonshine serve --transport ws --allow-actions --agent external
```
*Note:* `--allow-actions` enables mutating verbs like `speak` and `session.*`. `--agent external` disables the built-in Gemini LLM agent so your external program handles actions.

### 2. Run a Tier 1 external agent

- **Python Agent ([`samples/python-agent/`](python-agent/)):**
  ```sh
  cd samples/python-agent
  python3 agent.py
  ```
  Say *"what time is it"* or *"stop listening"*. The Python agent sends `speak` or `session.pause` action JSON back over the WebSocket.

- **Browser Cascade FAQ ([`samples/browser-cascade-faq/`](browser-cascade-faq/)):**
  Open `samples/browser-cascade-faq/index.html` directly in a browser. Microphones captured via Web Audio `AudioWorklet` send PCM or receive `TTSAudioEvent` audio chunks for in-browser speech output.

### Action Request JSON Format

```json
{
  "id": "req-101",
  "verb": "speak",
  "args": { "text": "Hello, I am your local voice assistant." }
}
```

---

## Part 3: Build a Type-Safe Go Voice Agent with `pkg/agentflow` (Tier 2)

In Part 3, you build a production-grade, type-safe Go voice agent using `pkg/serveapi` and `pkg/agentflow`.

We will use **[`samples/go-cascade-faq`](go-cascade-faq/)** as our reference program. It runs an offline RAG voice FAQ answering questions about `docs/MISSION.md`.

### 1. Structure your AgentFlow DSL

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/ghchinoy/moonshine-go/pkg/agentflow"
    "github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

func newAgentFlow(sink serveapi.ActionSink) serveapi.AgentHandler {
    flow := agentflow.New()
    flow.ActionSink(sink)

    // Direct speech output back through the WebSocket action sink
    flow.SpeakWith(func(text string) error {
        args, _ := json.Marshal(serveapi.SpeakArgs{Text: text})
        _, err := flow.EmitAction(serveapi.ActionRequest{Verb: "speak", Args: args})
        return err
    })

    // Global control verbs (intercepted ahead of FAQ flows)
    flow.Always("stop listening", func(d *agentflow.Dialog) error {
        fmt.Println("[agent] pausing session")
        _, err := d.PauseListening()
        return err
    })

    flow.Always("resume listening", func(d *agentflow.Dialog) error {
        fmt.Println("[agent] resuming session")
        _, err := d.ResumeListening()
        return err
    })

    // Define conversation flows for FAQ retrieval
    retriever := serveapi.NewStaticRetriever(
        serveapi.Result{Title: "Mission", Snippet: "moonshine go brings back the classic voice cascade..."},
    )

    flow.ListenFor("mission", func(d *agentflow.Dialog) error {
        results, err := retriever.Retrieve(context.Background(), "mission")
        if err != nil || len(results) == 0 {
            return nil
        }
        return d.Say(results[0].Snippet)
    })

    // Unmatched fallback guidance
    flow.Otherwise(func(utterance string) {
        fmt.Println("[agent] no match -- try: mission or 'stop listening'")
    })

    return agentflow.NewHandlerAdapter(flow)
}
```

### 2. Connect the AgentRunner to WebSocket events

```go
agentHandler := newAgentFlow(sink)
runner := serveapi.NewAgentRunner(agentHandler, sink)
runner.Run(ctx, events)
```

### 3. Run the Go Cascade FAQ Agent

In Terminal 2:
```sh
cd samples/go-cascade-faq
go run . -addr ws://localhost:8765/ws
```
Ask *"tell me about the mission"* or say *"stop listening"*.

> **Callout: `pkg/agentflow` vs. `IntentMatcher` Regex**  
> For simple, single-shot control commands with zero model dependencies, `internal/serve.IntentMatcher` provides a 95-line regex rule engine. For structured, multi-turn conversation flows (`Say`, `Ask`, `Confirm`, `Choose`) and fuzzy semantic trigger phrase matching (see the [AgentFlow interactive explainer](https://moonshine.ai/agent-flow/)), use `pkg/agentflow`. See [`samples/GUIDE.md`](GUIDE.md#5-pattern-selection-guide-fast-path-intentmatcher-vs-agentflow-dsl-vs-llm-agent) for the complete decision matrix.

> **In-Process AgentFlow Alternative: `moonshine serve --agent agentflow`**  
> If you want to run AgentFlow logic directly inside the sidecar daemon without building a separate Go client process, `moonshine serve --agent agentflow --allow-actions` instantiates a built-in `pkg/agentflow` instance in-process (composed with `IntentMatcher` fast-paths), with built-in `"ping"` (responds "pong") and `"time"` flows — no external client program required.

---

## Part 4: Native In-Process Embedding & Cloud Hosting

Once your voice agent logic is working, you can choose how to deploy it:

### Option A: Native In-Process Embedding (`pkg/moonshine`)
If you do not want a separate `moonshine serve` daemon or network IPC, embed `pkg/moonshine` directly into your Go process:

- **[`samples/go-embedded`](go-embedded/):** In-process streaming STT via `moonshine.Load()` and `Transcriber.NewStream()`.
- **[`samples/mcp-transcribe`](mcp-transcribe/):** Expose STT as an embedded MCP tool for Claude Desktop / LLM hosts over stdio or HTTP.
- **[`samples/desktop-app`](desktop-app/):** Desktop Wails v2 GUI application with in-process STT.

### Option B: Cloud & Container Hosting
Run `moonshine serve` beyond your local machine in containers or air-gapped field setups using `--audio-source remote` and WebSocket/gRPC streaming. See [`docs/hosting.md`](../docs/hosting.md) for full deployment patterns.

---

## Summary & Next Steps

You have learned how to:
1. Subscribe to live transcript feeds (Tier 0).
2. Issue control & speech actions over WebSocket (Tier 1).
3. Build a type-safe Go voice agent with `pkg/serveapi` and `pkg/agentflow` (Tier 2).
4. Embed STT natively or host the sidecar daemon.

Explore the full sample catalog in [`samples/README.md`](README.md) and technical architecture in [`docs/MISSION.md`](../docs/MISSION.md)!
