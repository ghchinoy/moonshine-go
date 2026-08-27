# Hosting `moonshine serve`

`moonshine serve` was built as a local agentic voice sidecar — one operator,
one machine, one microphone. But the same daemon can be **hosted**: run it on
a box (or in a cluster) so that browsers, apps, or other services connect to
it over the network. This doc explains what's already in place for that, what
you have to build, and the deployment shapes we recommend.

If you haven't read it yet, [MISSION.md](MISSION.md) is the "why," and
[../samples/](../samples/) is the hands-on developer walkthrough. This doc
is the "how do I run it for others."

> **Hosting is a deployment option, not a change of thesis.** The default and
> the soul of moonshine-go stay local-first: audio dies at the microphone,
> nothing leaves the box unless you decide it does. Hosting — especially
> bring-your-own-cloud — keeps that promise: *your* audio stays in *your*
> environment. See "Positioning" below.

---

## What's already hosting-shaped

The `serve` architecture splits cleanly into a **generic transport layer** and
the pipeline behind it. The transport half is already a server:

- **The STT engine is stateless and PCM-in.** `Stream.AddAudio([]float32,
  sampleRate)` and `Stream.Transcribe()` (`internal/moonshine/stt.go`) take
  arbitrary PCM. Nothing about the model requires a physical microphone — it
  just needs float32 samples at the target sample rate.
- **The serve layer already speaks over the wire.** The Hub fans transcript
  events out to N subscribers; the WebSocket and gRPC transports serialize
  them as JSON/proto (`internal/serve/`). This is exactly the surface you'd
  expose to remote clients.
- **The extension surface is IPC/JSON, in any language.** Subscribers and
  external agents talk to the daemon over WS/gRPC — they don't import Go. That
  was always a hosting-friendly decision.

In other words: the *smart half* of the daemon is ready to be hosted.

---

## Core pipeline edges (shipped)

Three edges that previously assumed a single local box have been decoupled for network deployment:

1. **Audio in — decoupled from the local mic (`--audio-source remote`).**
   `session.NewLive` takes `serveapi.AudioSource`. `moonshine serve --audio-source remote` accepts binary PCM streaming frames (Float32 or Int16, with automatic 48kHz→16kHz linear resampling) over WebSocket or gRPC directly into `RemoteAudioSource`. No physical microphone or cgo toolchain is required on the server.

### Browser as the audio source (JS/Lit front-end)

A natural deployment: a web page uses the browser's own microphone APIs
(`getUserMedia` + an `AudioWorklet`) to capture audio and streams it to a
`moonshine serve` backend over WebSocket; the server does the transcription
and streams `TranscriptEvent`s back on the same socket. The browser owns
*capture*; the server owns *inference*.

> **Status:** Both *transcript-out* and *audio-in* work today — see
> [../samples/browser-listen](../samples/browser-listen) for a zero-install
> HTML/JS sample that captures mic audio via `AudioWorklet`, streams PCM over
> WebSocket to `moonshine serve --audio-source remote`, and renders live
> transcript events.

**Why this split helps.** It removes the microphone — and therefore the cgo
build requirement — from the server entirely. Today `serve` needs
`CGO_ENABLED=1` and `gen2brain/malgo` *only* because of local mic capture
(`internal/audio`); the `internal/moonshine` STT bindings are cgo-free. If the
browser captures audio, a hosted server can be a pure PCM-in service with no
mic code, simplifying the container (this is the "mic removed" note in
*serve-in-a-box* below). It also hands mic-permission and cross-platform
device handling to the browser, which already solves those well.

**The sample-rate gotcha.** Browsers capture at the hardware's native rate —
commonly **48 kHz** — but Moonshine expects **16 kHz mono float32**
(`audio.TargetSampleRate` / `moonshine.SampleRate`, both `16000`). Something
must downsample. Two options:

- **Client-side (recommended):** resample to 16 kHz in the `AudioWorklet`
  before sending. Cuts upload bandwidth ~3× and keeps the server dumb.
- **Server-side:** send native-rate audio and resample on ingest. A pure-Go
  linear resampler already exists (`audio.Resample`), though it currently
  lives in the cgo-tainted `internal/audio` package; a hosted server would
  want it (or an equivalent) available cgo-free.

**Wire framing.** `Stream.AddAudio(samples []float32, sampleRate int32)` is the
target, so the simplest framing is **raw little-endian PCM frames** (Float32
or Int16) sent as WebSocket *binary* messages, with the sample rate agreed
once at connect time. Transcript events keep coming back as the existing JSON
text frames (`wireEnvelope{ "kind": "transcript"|"display"|"action_result",
"payload": … }`). Opus/WebM via `MediaRecorder` is possible for
bandwidth-constrained links but requires a server-side decoder — heavier;
prefer raw PCM unless you have a reason not to. Defining this framing is an
explicit part of `moonshine-go-elj`'s acceptance criteria.

**New problems to plan for.** This shape introduces concerns the local case
doesn't:

- **Ingest backpressure.** The Hub's drop-oldest backpressure is designed for
  *transcript fan-out*, not *audio ingest*. Dropping inbound audio frames
  corrupts the transcript, so ingest needs its own bounded-buffer / flow
  strategy — an open design point for `moonshine-go-elj`.
- **Secure context.** Browsers only allow `getUserMedia` on `https://` (or
  `localhost`), so any real deployment needs TLS in front of the endpoint.
- **Auth.** A browser-facing audio endpoint is a public attack surface; put
  authentication in front of the transports (see *Security* below).

**Minimal Lit sketch** (illustrative; the audio-in half assumes
`moonshine-go-elj` has landed):

```js
import { LitElement, html } from 'lit';

// AudioWorklet processor: downsample 48k -> 16k mono and post Float32 frames.
const workletCode = `
class PCMDownsampler extends AudioWorkletProcessor {
  constructor() { super(); this._ratio = sampleRate / 16000; this._acc = 0; this._buf = []; }
  process(inputs) {
    const ch = inputs[0][0];
    if (!ch) return true;
    for (let i = 0; i < ch.length; i++) {
      this._acc += 1;
      if (this._acc >= this._ratio) { this._acc -= this._ratio; this._buf.push(ch[i]); }
    }
    if (this._buf.length >= 1024) {
      this.port.postMessage(Float32Array.from(this._buf));
      this._buf = [];
    }
    return true;
  }
}
registerProcessor('pcm-downsampler', PCMDownsampler);
`;

export class MoonshineMic extends LitElement {
  static properties = { url: { type: String }, lines: { state: true } };
  constructor() { super(); this.url = 'wss://localhost:8765/ws'; this.lines = []; }

  async start() {
    this.ws = new WebSocket(this.url);
    this.ws.binaryType = 'arraybuffer';
    this.ws.onmessage = (ev) => {
      // Transcript-out: works today.
      const env = JSON.parse(ev.data);
      if (env.kind === 'transcript') {
        const p = JSON.parse(env.payload);
        const done = new Set(p.finalized_line_ids || []);
        this.lines = p.lines.filter((l) => done.has(l.id)).map((l) => l.text);
      }
    };

    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    const ctx = new AudioContext();
    const blobUrl = URL.createObjectURL(new Blob([workletCode], { type: 'text/javascript' }));
    await ctx.audioWorklet.addModule(blobUrl);
    const src = ctx.createMediaStreamSource(stream);
    const node = new AudioWorkletNode(ctx, 'pcm-downsampler');
    node.port.onmessage = (e) => {
      // Audio-in: forward-looking (needs moonshine-go-elj).
      if (this.ws.readyState === WebSocket.OPEN) this.ws.send(e.data.buffer);
    };
    src.connect(node);
  }

  render() {
    return html`
      <button @click=${() => this.start()}>Start mic</button>
      <ul>${this.lines.map((t) => html`<li>${t}</li>`)}</ul>
    `;
  }
}
customElements.define('moonshine-mic', MoonshineMic);
```

2. **Audio out — in-protocol audio events & barge-in.**
   `TTSSpeaker` emits `TTSAudioEvent` (`kind: tts_audio`) over transports with
   raw PCM audio data, state (`start`, `chunk`, `end`, `interrupted`), and sample rate.
   Remote clients receive synthesized audio over the network, and `session.barge_in`
   actions trigger in-protocol interrupts (`TTSSpeaker.Interrupt`).

3. **Sessions — one per connection (via `--max-sessions`).**
   In remote audio mode (`--audio-source remote`), `moonshine serve` uses a
   `SessionManager` to allocate a fresh `Stream`, Hub, and `Dispatcher` for
   each connecting WS/gRPC client, with resource limits enforced via
   `--max-sessions N` (default 10). When the cap is reached, new connection
   attempts are rejected immediately (WebSocket status 1008 / gRPC `ResourceExhausted`).
   Local mic mode (`--audio-source local`) retains single-broadcast session behavior.

Each of these is tracked as real work — see the `Hostable cascade` epic in bd
(linked at the bottom).

---

## Remote Audio Client Guidelines (Chunking, Pacing & Endpointing)

When connecting external clients (Go, Python, browser JS, Swift, or IoT hardware) to `moonshine serve --audio-source remote`, observe the following operational expectations:

### 1. Chunk Sizing and Ingestion Pacing
`moonshine serve` uses an asynchronous streaming pipeline with an internal polling interval (default 250ms).
- **Recommended Chunk Size:** Transmit audio in **50ms to 100ms frames** (1,600 to 3,200 bytes for 16kHz 16-bit mono PCM, or 3,200 to 6,400 bytes for 16kHz Float32). Chunks larger than 500ms increase latency; micro-chunks (<10ms) create unnecessary network framing overhead.
- **Pacing:** Pace transmission at **1x real-time speed** (e.g. transmit one 100ms chunk every 75–100ms). Blasting an entire pre-recorded audio file over TCP faster than real-time can outrun the internal poll loop and VAD lookahead buffers.

### 2. VAD Endpointing & Trailing Silence
Streaming Speech-to-Text models emit interim (in-progress) text while speech is active and only mark a line finalized (`is_complete: true`) when an acoustic silence boundary is detected.
- When streaming pre-recorded audio files or finite voice clips, append **1.0 to 1.5 seconds of trailing zero-PCM silence** after the speech payload.
- This silence allows the VAD to detect the end of speech and finalize the last sentence before the client closes the socket.

### 3. WAV Header Stripping
Strip the 44-byte RIFF header from `.wav` files before transmitting; WebSocket binary frames must contain only raw linear PCM samples.

### 4. Wire Types (`uint64` Line IDs)
`Line.ID` and `finalized_line_ids` in `TranscriptEvent` are **64-bit unsigned integers (`uint64`)**. In client languages (Go, Java, Rust, Swift), always decode these fields as unsigned 64-bit integers to avoid signed integer overflow on long-running sessions.

### 5. Streaming TTS Playback & Gapless Scheduling
When `moonshine serve` synthesizes speech for remote clients (via `speak` actions), it delivers audio as a **stream of `TTSAudioEvent` frames** (`state: "start"`, followed by N `state: "chunk"` frames, ending with `state: "end"` or `state: "interrupted"` on barge-in).
- **Sequential Scheduling:** Clients must schedule consecutive chunk buffers along their audio timeline (e.g. `nextPlayTime = Math.max(audioCtx.currentTime, nextPlayTime) + buffer.duration` in the Web Audio API) to ensure gapless, uninterrupted playback without overlapping frames.
- **Barge-in Interruption:** On receiving `state: "interrupted"`, clients should immediately cancel and stop any currently queued audio buffers.

### 6. Reference Implementations
- **Go:** See **[samples/go-stream-audio](../samples/go-stream-audio/)** for a complete Go client implementing paced streaming, trailing silence, and bidirectional WebSocket receiving.
- **Browser (JS):** See **[samples/browser-listen](../samples/browser-listen/)** for in-browser microphone capture and streaming via `AudioWorklet`.
- **Browser Voice Agent (JS):** See **[samples/browser-cascade-faq](../samples/browser-cascade-faq/)** for bidirectional audio capture, live transcript actions, and gapless streaming TTS playback via the Web Audio API.

---

## Deployment shapes

There is **one** "hostable cascade" architecture. What differs between the
shapes below is packaging and hardening, not the core.

### 1. Serve-in-a-box (demos, edge appliances, on-prem)
An opinionated container image of `moonshine serve` — `libmoonshine` +
onnxruntime bundled, the local mic removed (there's no mic in a pod), audio
arriving from clients over the network. Good for demos, a voice appliance on a
single on-prem box, or an edge device. Lowest effort: mostly the
`AudioSource`/TTS-to-bytes work plus a Dockerfile and run docs.

### 2. Bring-your-own-cloud / self-host (the recommended hosted shape)
The same image, plus the operational hardening a real deployment needs:
configuration and auth hooks, health/readiness endpoints, resource limits,
and deploy artifacts (e.g. a Helm chart). The key property: **the customer
runs it in their own cloud/VPC**, so their audio never leaves their
environment. This is the shape that preserves the privacy thesis while still
being "hosted," and it's the one we point regulated / on-prem users toward.

### 3. Multi-tenant SaaS (left as an exercise)
You *can* operate a public endpoint where customers send you audio. That turns
moonshine-go into an online STT service and brings the full operator burden —
tenant isolation and auth, quotas and billing, autoscaling, uptime, and the
compliance surface (SOC 2 / HIPAA / BAA and friends) that comes with holding
other people's audio. We don't ship this, and it partly inverts the
local-first thesis, but nothing in the architecture forbids it: the session
manager, transports, and stateless engine are the building blocks. **If you
have a use case for it, it's a supported thing to build on top — just outside
what this project operates.**

---

## Operational notes

- **Native footprint.** The container needs `libmoonshine` and its onnxruntime
  dependency. Server-side you *remove* the mic (malgo/cgo), which actually
  simplifies the build — the STT bindings themselves are cgo-free.
- **Providers at scale.** `--providers` selects onnxruntime execution
  providers (CPU, CoreML, etc.). Choose per host; benchmark before assuming a
  GPU/accelerator helps for short-form streaming (see
  [hardware-acceleration.md](hardware-acceleration.md)).
- **Cost model.** Local/self-hosted STT+TTS is fixed-cost hardware, not
  per-audio-minute metering. That's a feature to lead with over cloud STT
  vendors — but you own capacity planning: memory and model-load cost grow per
  concurrent session.
- **Security.** `--allow-actions` gates mutating verbs (`speak`, session
  control, `run_command`), and `run_command` is off by default. Treat the
  action surface as untrusted input in any hosted deployment, and put auth in
  front of the transports.

---

## Positioning: hosting vs. the local-first thesis

Hosting moonshine-go, in the SaaS shape, makes you look like the online STT
vendors it was meant to contrast with — you become the party receiving
customers' audio. That's worth being clear-eyed about. But the
**bring-your-own-cloud** shape is different in kind: it's still "your audio
stays with you," just deployed on infrastructure the customer controls rather
than a single laptop. That's why we treat hosting as *another deployment mode
of the same pipeline* — local by default, self-hostable when you need it,
and operate-it-yourself SaaS if a real requirement shows up — without moving
the project off its local-first center of gravity.

---

## See also

- [MISSION.md](MISSION.md) — why the cascade, and the local-first default.
- [../samples/](../samples/) — Tier 0/1/2 developer walkthrough of the
  serve API you'd be hosting, with runnable Go and Python examples.
- [serve-sidecar.md](serve-sidecar.md) — the serve architecture / IPC contract.
- [hardware-acceleration.md](hardware-acceleration.md) — `--providers` and
  measured acceleration results.
- bd epic `Hostable cascade` (`moonshine-go-f26`) — the tracked enabling work.
  The browser-audio-source path specifically is `moonshine-go-elj` (remote-PCM
  `AudioSource`) + `moonshine-go-7br` (per-connection sessions).
