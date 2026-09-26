# samples/go-embedded — in-process Speech-to-Text & Text-to-Speech via `pkg/moonshine`

In-process batch and streaming Speech-to-Text (STT) and Text-to-Speech (TTS) directly in Go, using
[`pkg/moonshine`](../../pkg/moonshine) — the public, pure-Go (`CGO_ENABLED=0`)
reference binding over `libmoonshine`'s C ABI.

This sample demonstrates **Native In-Process Embedding**: linking `libmoonshine` and running STT and TTS directly inside your own Go application process via `pkg/moonshine` without a `moonshine serve` daemon or network IPC connection.

## Sample Rating

| Axis | Rating / Details |
|---|---|
| **Tier** | Native / in-process (no daemon) |
| **Complexity** | 2/5 |
| **Setup Cost** | Medium (requires local `libmoonshine.{dylib,so}` + downloaded STT or TTS models) |
| **Pillars** | Privacy, Composability |
| **Industry / Use Case** | Embedded Applications, Desktop Dictation, Voice Agents, CLI Tooling |
| **Appeal** | 5/5 |

---

## What it demonstrates

1. **Direct `pkg/moonshine` usage** — loads `libmoonshine` dynamically at runtime
   via `ebitengine/purego` (`moonshine.Load()`) without requiring a C toolchain (`cgo`) to build.
2. **In-process batch transcription** — transcribes a `.wav` audio file in-process
   using `tr.Transcribe()`, returning structured `Line` text, word timings,
   speaker labels, and mean confidence.
3. **In-process streaming STT (`NewStream`)** — ingests live PCM audio chunks
   (100ms PCM buffers) via `stream.AddAudio()` and `stream.Transcribe()`,
   demonstrating incremental streaming speech recognition directly inside a Go application.
4. **In-process streaming TTS (`NewStream`) vs one-shot comparison** — synthesizes spoken text
   using `moonshine.NewSynthesizer()`, measuring and comparing **Time-to-First-Audio (TTFA)** between
   traditional one-shot generation (`synth.Synthesize`) and sub-sentence streaming (`synth.NewStream`).
5. **Zero daemon dependency** — operates completely self-contained without
   `moonshine serve` or WebSocket/gRPC network sockets.

---

## Quickstart

### 1. Build or fetch `libmoonshine`

Ensure `libmoonshine.{dylib,so}` is available locally and point `MOONSHINE_LIB_DIR` at it (for production bundling without environment variables, see [`docs/bundling-libmoonshine.md`](../../docs/bundling-libmoonshine.md)):

```sh
cd ../.. # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib" # see repo README
```

### 2. Run in-process Text-to-Speech (TTS) with TTFA comparison

Compare one-shot synthesis against incremental streaming Time-to-First-Audio:

```sh
cd samples/go-embedded

# Synthesize speech using default Kokoro voice:
go run . -tts "Streaming transcription and speech synthesis make voice agents viable again."
```

Example output:

```
=== In-Process TTS Synthesis (One-Shot vs Streaming TTFA) ===
Text:   "Streaming transcription and speech synthesis make voice agents viable again."
Voice:  kokoro_af_heart (24000Hz)

Metric                   One-Shot           Streaming (NewStream)    Speedup       
------------------------------------------------------------------------------------
Time-to-First-Audio      1127ms             292ms                    3.9x faster   
Total Generation Time    1127ms             1454ms                   -             
Audio Duration           5.42s (130200 smp) 5.42s (130200 smp)       -             
Saved WAV File           tts_oneshot.wav    tts_streaming.wav        -             
```

Both outputs are exported as 16-bit PCM `.wav` files (`tts_oneshot.wav` and `tts_streaming.wav`).

### 3. Run in-process Speech-to-Text (STT)

Ensure an STT model is downloaded:

```sh
../../bin/moonshine setup --language en --arch tiny
```

Batch transcription over a WAV file:

```sh
go run . -audio /path/to/recording.wav
```

Streaming chunked transcription (`NewStream`):

```sh
go run . -audio /path/to/recording.wav -stream
```

### Flags

```sh
# STT flags:
go run . -audio /path/to/recording.wav -word-timestamps    # Enable per-word timings
go run . -audio /path/to/recording.wav -identify-speakers   # Enable speaker diarization

# TTS flags:
go run . -tts "Hello world" -tts-voice kokoro_af_heart     # Select Kokoro/Piper voice
go run . -tts "Hello world" -tts-out-dir /path/to/output    # Destination directory for WAV files
go run . -tts "Hello world" -tts-g2p-root /path/to/models  # Custom TTS model directory
```
