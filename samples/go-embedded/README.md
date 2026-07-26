# samples/go-embedded — in-process Speech-to-Text via `pkg/moonshine`

In-process batch and streaming Speech-to-Text directly in Go, using
[`pkg/moonshine`](../../pkg/moonshine) — the public, pure-Go (`CGO_ENABLED=0`)
reference binding over `libmoonshine`'s C ABI.

This sample demonstrates **Native In-Process Embedding**: linking `libmoonshine` and running STT directly inside your own Go application process via `pkg/moonshine` without a `moonshine serve` daemon or network IPC connection.

## Sample Rating

| Axis | Rating / Details |
|---|---|
| **Tier** | Native / in-process (no daemon) |
| **Complexity** | 2/5 |
| **Setup Cost** | Medium (requires local `libmoonshine.{dylib,so}` + downloaded STT model) |
| **Pillars** | Privacy, Composability |
| **Industry / Use Case** | Embedded Applications, Desktop Dictation, CLI Tooling |
| **Appeal** | 4/5 |

---

## What it demonstrates

1. **Direct `pkg/moonshine` usage** — loads `libmoonshine` dynamically at runtime
   via `ebitengine/purego` (`moonshine.Load()`) without requiring a C toolchain (`cgo`) to build.
2. **In-process batch transcription** — transcribes a `.wav` audio file in-process
   using `tr.Transcribe()`, returning structured `Line` text, word timings,
   speaker labels, and mean confidence.
3. **In-process streaming API (`CreateStream`)** — ingests live PCM audio chunks
   (100ms PCM buffers) via `stream.AddAudio()` and `stream.Transcribe()`,
   demonstrating incremental streaming speech recognition directly inside a Go application.
4. **Zero daemon dependency** — operates completely self-contained without
   `moonshine serve` or WebSocket/gRPC network sockets.

---

## Quickstart

### 1. Build or fetch `libmoonshine`

Ensure `libmoonshine.{dylib,so}` is available locally and point `MOONSHINE_LIB_DIR` at it (for production bundling without environment variables, see [`docs/bundling-libmoonshine.md`](../../docs/bundling-libmoonshine.md)):

```sh
cd ../.. # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib" # see repo README
```

### 2. Download an STT model

Ensure an STT model is downloaded (e.g., `tiny-en` or `tiny-streaming-en`):

```sh
./bin/moonshine setup --language en --arch tiny
```

### 3. Run batch in-process transcription

```sh
cd samples/go-embedded

# Run batch in-process transcription over a WAV file:
go run . -audio /path/to/recording.wav
```

### 4. Run streaming in-process transcription

```sh
# Demonstrate stream chunking (CreateStream API):
go run . -audio /path/to/recording.wav -stream
```

### Flags

```sh
# Enable per-word timestamps:
go run . -audio /path/to/recording.wav -word-timestamps

# Enable speaker diarization:
go run . -audio /path/to/recording.wav -identify-speakers
```
