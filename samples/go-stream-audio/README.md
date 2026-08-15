# samples/go-stream-audio — Bidirectional PCM Audio Streaming over WebSocket

A standalone Go client demonstrating how to stream raw 16kHz linear PCM audio chunks over WebSocket binary frames to `moonshine serve --audio-source remote` while concurrently receiving and decoding live `TranscriptEvent` JSON envelopes on the same connection.

This sample addresses the practical integration patterns discovered when building hardware audio bridges (such as the [OMI](https://github.com/BasedHardware/omi) wearable device integrations in `omi-stt-go`).

---

## What It Demonstrates

```
WAV file / Mic ──PCM chunks (Binary WS)──▶ moonshine serve (--audio-source remote)
                                                     │
                                             RemoteAudioSource
                                                     │
                                          Streaming STT Pipeline
                                                     │
Terminal stdout ◀──TranscriptEvents (JSON WS)────────┘
```

1. **Bidirectional WebSocket Multiplexing:** Outbound binary frames (`websocket.MessageBinary`) stream raw audio into the daemon, while inbound text frames (`{"kind": "transcript", "payload": {...}}`) stream live transcripts back to the client across a single connection.
2. **Chunk Sizing & Cadence Pacing:** Audio is chunked into 100ms frames (3,200 bytes for 16kHz 16-bit mono PCM) and paced at real-time rate (`time.Ticker`).
3. **VAD Endpointing via Trailing Silence:** Appending ~1.5s of zero-PCM silence after speech triggers the streaming model's Voice Activity Detector (VAD) to detect utterance boundaries and finalize the last sentence (`is_complete: true`).
4. **Header Stripping:** Automatically parses RIFF headers to extract raw linear PCM samples from standard `.wav` files.
5. **Idempotency & Uint64 Wire Types:** Safely decodes 64-bit unsigned line IDs (`uint64`) and uses `finalized_line_ids` to deduplicate streaming updates.

---

## Sample Rating

| Axis | Rating / Details |
|---|---|
| **Tier** | Tier 0 / Tier 1 (Remote Audio Source) |
| **Complexity** | 2/5 |
| **Setup Cost** | Low (offline, local `moonshine serve`) |
| **Demonstrated Pillars** | Composability, Privacy, Observability |
| **Primary Vertical / Use Case** | Hardware Wearables, VoIP Audio Streaming, Network Microphone Capture |
| **Appeal** | 5/5 |

---

## Remote Audio Streaming Best Practices

### 1. Chunk Sizing and Ingestion Pacing
`moonshine serve` uses an asynchronous streaming pipeline with an internal polling interval (default 250ms).
- **Chunk Size:** Send audio in **50ms to 100ms frames** (1,600 to 3,200 bytes for 16-bit 16kHz mono). Sending chunks larger than 500ms degrades real-time responsiveness; sending tiny micro-frames (<10ms) increases WebSocket framing overhead.
- **Pacing:** Pace chunk transmission to match real-time audio playback (1x speed). Dumping an entire 60-second audio file in a burst can outrun the internal poll loop and VAD lookahead buffers.

### 2. VAD Endpointing & Trailing Silence
Streaming Speech-to-Text models emit interim (in-progress) text while speech is active and only mark a line finalized (`is_complete: true`) when an acoustic silence boundary is detected.
- When streaming pre-recorded audio files or finite voice clips, append **1.0 to 1.5 seconds of trailing zero-PCM silence** after the speech payload.
- This silence allows the VAD to detect the end of speech and finalize the last sentence before the client closes the socket.

### 3. Wire Types (`uint64` Line IDs)
`Line.ID` and `finalized_line_ids` in `TranscriptEvent` are **64-bit unsigned integers (`uint64`)**. In client languages (Go, Java, Rust, Swift), always decode these fields as unsigned 64-bit integers to avoid signed integer overflow on long-running sessions.

---

## Quickstart

### 1. Start `moonshine serve` with a remote audio source

In Terminal 1:

```sh
cd ../..  # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
./bin/moonshine serve --transport ws --addr :8765 \
  --audio-source remote \
  --remote-audio-encoding int16 \
  --remote-audio-rate 16000 \
  --remote-audio-channels 1
```

### 2. Stream audio from Go

In Terminal 2:

```sh
cd samples/go-stream-audio

# Stream a local 16kHz WAV file:
go run . -addr ws://localhost:8765/ws -input /path/to/recording.wav

# Or run without -input to stream a generated test tone:
go run . -addr ws://localhost:8765/ws
```

### CLI Flags

| Flag | Default | Description |
|---|---|---|
| `-addr` | `ws://localhost:8765/ws` | Moonshine serve WebSocket URL |
| `-input` | `""` | Path to 16kHz 16-bit mono WAV file to stream (optional) |
| `-chunk-ms` | `100` | Duration in ms per streamed binary frame (3,200 bytes) |
| `-silence-ms` | `1500` | Duration in ms of trailing zero-PCM silence for VAD endpointing |
| `-pace` | `1.0` | Streaming pace multiplier (1.0 = real-time) |
| `-debug` | `false` | Print verbose chunk-by-chunk send and poll diagnostic traces |
