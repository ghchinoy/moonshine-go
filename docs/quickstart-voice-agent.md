# Quickstart: Build Your First Voice Agent against `moonshine serve`

This tutorial walks through building a voice-controlled agent using `moonshine serve`.

`moonshine serve` runs a local STT + TTS pipeline as an **agentic voice sidecar** that streams live transcripts over WebSocket or gRPC and executes inbound actions (`speak`, `display`, `session.pause/resume/stop`, `run_command`).

---

## Architecture Overview

```
mic → session.Live ──Update──▶ Hub ──event(JSON)──▶ Transports (WS + gRPC) ──▶ Subscribers
                                 ▲                          │
              action(JSON) ──────┘◀─────────────────────────┘
                                 │
                          Dispatcher ──▶ Agent (Gemini or External IPC Subscriber)
                                 │            tools: lookup/retrieve, display_card, speak
                                 ├─ TTS  (Synthesizer → PlayFloat32, mic-mute barge-in guard)
                                 └─ session control (pause/resume/stop)
```

---

## 1. Start the Sidecar Daemon

Start `moonshine serve` with WebSocket and gRPC transports enabled, along with action execution permissions:

```bash
moonshine serve --transport ws,grpc --addr :8765 --grpc-addr :9090 --allow-actions --agent external
```

---

## Tier 0: Subscribe to Live Transcripts (Any Language)

Connect to the WebSocket endpoint (`ws://localhost:8765/ws`) to receive real-time transcript events.

### Invariants to Honor
- **Backpressure = Drop Oldest:** The sidecar drops intermediate interim frames for slow subscribers.
- **Finalized-Once Idempotency:** Check `finalized_line_ids` or deduplicate on `Line.ID` + `IsComplete=true`.

### Python Example (Tier 0)

```python
import asyncio
import json
import websockets

async def listen():
    uri = "ws://localhost:8765/ws"
    async with websockets.connect(uri) as ws:
        print("Connected to moonshine serve")
        seen_finalized = set()

        async for message in ws:
            env = json.loads(message)
            if env.get("kind") == "transcript":
                payload = json.loads(env["payload"])
                
                # Check for newly finalized line IDs
                finalized_ids = payload.get("finalized_line_ids", [])
                for line in payload.get("lines", []):
                    if line["id"] in finalized_ids and line["id"] not in seen_finalized:
                        seen_finalized.add(line["id"])
                        print(f"[FINAL] Speaker {line.get('speaker_id', 0)}: {line['text']}")

asyncio.run(listen())
```

---

## Tier 1: External Agent via Action Requests

External agents receive finalized lines over WebSocket and send `ActionRequest` JSON payloads back to the sidecar.

### Action Request JSON Format

```json
{
  "id": "req-001",
  "verb": "speak",
  "args": {
    "text": "Hello, I am your external voice assistant."
  }
}
```

### Supported Action Verbs

| Verb | Args Payload | Action Effect |
|---|---|---|
| `speak` | `{"text": "...", "voice": "...", "speed": 1.0}` | Synthesizes speech and plays audio through default output device |
| `display` | `{"title": "...", "body": "...", "kind": "info"}` | Fans out a structured `DisplayCard` event to all connected UI subscribers |
| `session.pause` | *(none)* | Mutes mic input and pauses live transcription |
| `session.resume` | *(none)* | Resumes mic input and transcription |
| `session.stop` | *(none)* | Stops the sidecar daemon session |

### Python External Agent Example (Tier 1)

```python
import asyncio
import json
import websockets

async def run_agent():
    uri = "ws://localhost:8765/ws"
    async with websockets.connect(uri) as ws:
        print("External agent connected")
        seen_lines = set()

        async for message in ws:
            env = json.loads(message)
            if env.get("kind") != "transcript":
                continue

            payload = json.loads(env["payload"])
            for line_id in payload.get("finalized_line_ids", []):
                if line_id in seen_lines:
                    continue
                seen_lines.add(line_id)

                # Find line text
                for line in payload.get("lines", []):
                    if line["id"] == line_id:
                        text = line["text"].strip().lower()
                        print(f"Agent received: {text}")

                        # Trigger voice action
                        if "time" in text:
                            action = {
                                "id": f"act-{line_id}",
                                "verb": "speak",
                                "args": {"text": "The current time is 2 PM."}
                            }
                            await ws.send(json.dumps(action))

asyncio.run(run_agent())
```

---

## Tier 2: In-Process Go Extension

For in-process Go applications, you can build custom agents using `serve.AgentHandler`, `serve.Retriever`, and `serve.LLMClient`.

### 1. Implement `AgentHandler`

```go
package main

import (
	"context"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
)

type MyCustomAgent struct{}

func (a *MyCustomAgent) OnFinalizedLine(ctx context.Context, line moonshine.Line) []event.ActionRequest {
	if line.Text == "hello sidecar" {
		args, _ := json.Marshal(event.SpeakArgs{Text: "Hello from in-process Go agent!"})
		return []event.ActionRequest{
			{Verb: "speak", Args: args},
		}
	}
	return nil
}
```

### 2. Combine Fast-Path Intent Matching and LLMs

Use `serve.CompositeHandler` to evaluate deterministic regex rules first, falling back to an LLM agent like Gemini on a miss:

```go
intentMatcher := serve.NewIntentMatcher() // handles "stop listening", "say <text>", etc.
geminiAgent := serve.NewGeminiAgent(serve.GeminiAgentOptions{
	Model:     "gemini-2.5-flash",
	Client:    realGeminiClient,
	Retriever: serve.NewStaticRetriever(...),
})

compositeAgent := serve.NewCompositeHandler(intentMatcher, geminiAgent)
agentRunner := serve.NewAgentRunner(compositeAgent, dispatcher)
```

---

## See Also
- [docs/serve-sidecar.md](serve-sidecar.md) — Architectural contract and file ownership map
- [docs/user-guide.md](user-guide.md) — CLI flags and configuration reference
