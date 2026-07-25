# samples/browser-cascade-faq — offline browser voice agent, zero install

A complete, zero-install, Tier 1 voice FAQ agent in JavaScript: captures your
microphone in the browser via `AudioWorklet`, streams PCM audio to
`moonshine serve`, matches spoken questions against `MISSION.md` content directly in
the browser tab, sends `speak` ActionRequests back over WebSocket, and plays
the returned synthesized `TTSAudioEvent` speech back through your laptop speakers
using the Web Audio API.

No build step, no framework, no server-side agent process in any language —
open `index.html` in a browser and the tab itself is the agent.

## What it demonstrates

- **Composability, maximally** — the strongest proof of the composability
  pillar from [docs/MISSION.md](../../docs/MISSION.md) in this repo: a static
  HTML page with no language runtime beyond what's built into every browser
  implements a full Tier 1 voice agent against `pkg/serveapi`'s wire contract.
- **Bi-directional remote voice cascade** — remote audio in (`AudioWorklet`
  int16 PCM binary frames) + remote TTS speech out (`TTSAudioEvent` float32 PCM
  decoded and played via Web Audio `AudioContext`).
- **Observability in the browser** — an on-page event log displaying wall-clock
  timestamps, STT latency (`Line.LastLatencyMs`), keyword hits/misses, and
  action dispatch round trips live.
- **Control** — fast-path regex commands ("stop listening", "resume listening")
  sending `session.pause`/`session.resume` actions back to the sidecar.

## Run it

Build/fetch `libmoonshine` first if you haven't (see repo root
[README](../../README.md)). Then, in one terminal, start `moonshine serve` with
remote audio and actions enabled:

```sh
cd ../..  # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
./bin/moonshine serve --transport ws --addr :8765 \
  --allow-actions \
  --agent external \
  --audio-source remote \
  --remote-audio-encoding int16 \
  --remote-audio-rate 16000 \
  --remote-audio-channels 1
```

Serve this directory over HTTP (browsers require HTTPS or `localhost` HTTP for `getUserMedia` mic access):

```sh
cd samples/browser-cascade-faq
python3 -m http.server 8080
```

Open `http://localhost:8080`, click **Connect**, then **Start speaking**.

Ask about **mission**, **cascade**, **privacy**, **control**,
**observability**, or **composability** — or say **"stop listening"** /
**"resume listening"**.

## Multi-Tenant Isolation & Resource Limits Demo

`moonshine serve`'s `SessionManager` (`#7br`) supports per-connection session isolation and a concurrency cap (`--max-sessions`). To verify multi-tenant isolation and enforcement:

1. Start `moonshine serve` with `--max-sessions 2`:

   ```sh
   ./bin/moonshine serve --transport ws --addr :8765 \
     --allow-actions --agent external --audio-source remote \
     --remote-audio-encoding int16 --remote-audio-rate 16000 \
     --max-sessions 2
   ```

2. Open `http://localhost:8080` in **Tab 1** and click **Connect** -> status displays `connected`.
3. Open `http://localhost:8080` in **Tab 2** and click **Connect** -> status displays `connected`.
   - Each tab runs an independent session with its own isolated transcript stream and TTS audio output.
4. Open `http://localhost:8080` in **Tab 3** and click **Connect** -> status displays `error connecting` / `disconnected (code 1008: serve: max session limit reached)`. The server enforces the session limit and rejects connection #3 cleanly.

## How it works

```
browser mic ──AudioWorklet──▶ int16 PCM ──WS binary frame──▶ moonshine serve
                                                                    │
                                                        RemoteAudioSource
                                                                    │
                                                               STT pipeline
                                                                    │
             browser JS agent  ◀──WS JSON transcript frame──────────┤
                     │                                              │
        keyword match / action request                              │
                     │                                              │
                     ├──WS JSON action frame (speak/session.*)─────▶│
                     │                                         Dispatcher
                     │                                              │
                     │                                         TTS Synthesizer
                     │                                              │
   Web Audio ◀──WS JSON TTSAudioEvent frame (float32 PCM)───────────┘
```

1. `worklet.js` captures Float32 PCM from the mic at 128-frame intervals,
   converts to int16 little-endian PCM, and posts buffers to the main thread.
2. `app.js` sends raw PCM buffers as binary WebSocket messages.
3. On finalized transcript lines, `app.js` checks for control commands
   (`stop listening` -> `session.pause`) or FAQ keywords (`mission` -> `speak`).
4. On a match, `app.js` sends an `ActionRequest` JSON frame (`{"verb": "speak", "args": {"text": "..."}}`).
5. When `moonshine serve` synthesizes TTS, it emits `TTSAudioEvent` JSON frames (`kind: "tts_audio"`).
6. `app.js` decodes `audio_data` (`[]float32`) from the TTS event, creates a Web Audio `AudioBuffer`, and plays it via `AudioBufferSourceNode`.

See [../README.md](../README.md) for the full Tier 0/1/2 walkthrough this sample is part of.
