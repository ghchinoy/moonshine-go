# Hosting `moonshine serve`

`moonshine serve` was built as a local agentic voice sidecar — one operator,
one machine, one microphone. But the same daemon can be **hosted**: run it on
a box (or in a cluster) so that browsers, apps, or other services connect to
it over the network. This doc explains what's already in place for that, what
you have to build, and the deployment shapes we recommend.

If you haven't read it yet, [MISSION.md](MISSION.md) is the "why," and
[quickstart-voice-agent.md](quickstart-voice-agent.md) is the hands-on
developer walkthrough. This doc is the "how do I run it for others."

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

## What you have to build

Three things assume a single local box today. Hosting means replacing each
with a networked equivalent. All three are at the **edges** of the pipeline —
the core model loop is untouched.

1. **Audio in — decouple from the local mic.**
   `runServe` calls `audio.StartMicCapture()` directly, and
   `session.NewLive(tr, mic *audio.MicCapture, …)` takes the *concrete*
   `MicCapture`. To accept a remote client's audio you need an `AudioSource`
   interface (a mic implementation for local use, a network-PCM
   implementation for hosted use) and to feed those samples into
   `Stream.AddAudio`. This refactor also unblocks file/stream transcription,
   so it's useful well beyond hosting.

2. **Audio out — return bytes, not speaker playback.**
   TTS today uses `audio.PlayFloat32` (the box's default output device), and
   barge-in is a local mic mute (`mic.SetMutedFunc`). A hosted daemon must
   instead **return synthesized audio over the transport** to the client, and
   express barge-in as a protocol signal rather than a local device mute. The
   synthesizer already exposes `Synthesize(...)` returning audio; `PlayFloat32`
   is simply the wrong primitive for the hosted case.

3. **Sessions — one per connection, not one per process.**
   Today one process owns one mic and one broadcast Hub — a single shared
   session fanned out to many viewers. Hosting multiple independent callers
   needs a **session manager**: each connection gets its own `Stream`, Hub,
   and agent, with lifecycle and resource limits (memory and model-load cost
   scale per active session).

Each of these is tracked as real work — see the `Hostable cascade` epic in bd
(linked at the bottom).

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
- [quickstart-voice-agent.md](quickstart-voice-agent.md) — Tier 0/1/2
  developer walkthrough of the serve API you'd be hosting.
- [serve-sidecar.md](serve-sidecar.md) — the serve architecture / IPC contract.
- [hardware-acceleration.md](hardware-acceleration.md) — `--providers` and
  measured acceleration results.
- bd epic `Hostable cascade` — the tracked enabling work (AudioSource,
  TTS-to-bytes, session manager, container, hosting docs).
