# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [v0.9.0] - 2026-07-26

### Added
- **ZipVoice Zero-Shot Voice Cloning**: Added `NewSynthesizerFromClone` in `pkg/moonshine` (binding `moonshine_create_tts_synthesizer_from_memory`) and `--clone <wav_path>` / `--clone-transcript <text>` flags to `moonshine tts` for zero-shot voice cloning using reference WAV audio clips (`#21v`).
- **Clone-Ready Audio Recording**: Added `--record-audio [path_or_dir]` to `moonshine live` (saving microphone capture to a WAV file upon session end with timestamped naming `moonshine_clip_YYYYMMDD-HHMMSS_16k_mono.wav`) and `--save-line-audio <dir>` to `moonshine live` and `moonshine transcribe` (exporting each finalized line's audio + transcript as individual `.wav` and `.txt` files) (`#4ai`).
- **Native WAV Sample-Rate Decoding**: Added `LoadFileWithSampleRate` in `internal/audio` to decode WAV files to mono float32 PCM while preserving native sample rate for ZipVoice cloning inputs (`#21v`).

---

## [v0.8.0] - 2026-07-25

### Added
- **Public `pkg/moonshine` Pure-Go Reference Binding**: Promoted `internal/moonshine` to public `pkg/moonshine`, making the pure-Go, cgo-free (`CGO_ENABLED=0` buildable) C-API binding layer importable by external Go packages and modules (`#i9l`).
- **Sample Rating Schema & Developer Guide**: Added 6-axis sample rating schema in `samples/CONTRIBUTING.md`, central narrative guide in `samples/GUIDE.md`, and contributor skill `skills/moonshine-sample-rating/` (`#0v6`).

### Changed
- **Sample Batch Engine Alignment**: Refactored `samples/go-bulk-analysis` to consume the native `v0.7.0` `moonshine transcribe --json <dir>` batch manifest directly (`#17w`).

---

## [v0.7.0] - 2026-07-25

### Added
- **Multi-File & Batch Transcription Engine**: Extended `moonshine transcribe` (`MinimumNArgs(1)`) to support processing multiple audio files, directories, globs, and GCS `gs://` prefixes/buckets with ONNX model instance reuse and bounded worker pool concurrency (`--concurrency` / `-c`, `--recursive` / `-r`) (`#hqv`, `#4mq`).
- **Batch Manifest JSON Schema**: Introduced structured batch manifest `--json` schema output containing overall batch summary metrics (`total_files`, `total_audio_sec`, `total_inference_ms`, `aggregate_rtf`, `confidence_summary`) and per-file results with per-line word timings, speaker spans, confidence scores, and latencies (`#38s`).
- **GCS Prefix Listing**: Added `ListPrefix` in `internal/gcsfetch` to recursively resolve GCS folder URIs into individual object targets (`#4mq`).
- **TUI Latency & Confidence Display**: Enhanced `moonshine live` TUI stats bar in `internal/tui` to prominently render per-line STT inference latency (`stt_lat`) and mean confidence (`conf`) (`#h2j`).
- **gRPC Line Protobuf Parity**: Added `last_latency_ms` (field 10) and `confidence` (field 11) to `Line` in `pkg/servepb/serve.proto` (`#31c`).

### Fixed
- **Per-File Batch Error Isolation**: Isolated per-file transcription and download failures in batch mode so individual corrupted files record an error status without aborting processing for remaining files (`#m77`).

---

## [v0.6.0] - 2026-07-24

### Added
- **Tunable Endpointing Policy**: Added `session.FinalizationPolicy` and `--endpoint-*` CLI flags (`--endpoint-post-final-delay`, `--endpoint-min-utterance-chars`, `--endpoint-max-utterance-duration`) to `moonshine serve` for session-layer finalization debouncing, utterance length thresholds, and max duration force-finalization (`#qii`).
- **Public `pkg/servepb` gRPC Stubs**: Relocated generated gRPC client/server stubs and `serve.proto` to public `pkg/servepb` package, enabling external Go consumers/modules to import `servepb.NewVoiceSidecarClient` (`#sz3`).
- **`--tts-play-local` Flag**: Added CLI flag to `moonshine serve` to explicitly enable/disable local server speaker playback during TTS synthesis (defaults to `true` for `--audio-source local`, `false` for `--audio-source remote`).
- **Per-Session TTS Isolation**: Multi-tenant `SessionManager` now isolates TTS speak-back routing per session, ensuring concurrent clients receive only their own synthesized audio frames.

### Fixed
- **Remote TTS Audio Return Wiring**: Fixed an issue where `moonshine serve` with `--audio-source remote` played synthesized audio on the server's local speaker instead of emitting `TTSAudioEvent` frames over WebSocket and gRPC transports (`#ifx`).

### Changed
- **`Speaker` Interface**: Updated `Speaker.Speak(ctx, pub, text, voice, speed)` in `internal/serve/dispatcher.go` to accept a per-call `Publisher` for transport routing without duplicating TTS model loads across sessions.

---

## [v0.5.1] - 2026-07-24

### Added
- **Confidence Scores**: Surfaced per-line and per-word recognition confidence scores (`Confidence` float32 field) on `Line` and `Word` in `pkg/serveapi`.

### Fixed
- **`AgentRunner` Deadlock**: Decoupled `ActionSink.Dispatch` execution in `AgentRunner` to prevent deadlocks when an agent emits synchronous actions during event handling (`#jwh`).

---

## [v0.5.0] - 2026-07-24

### Added
- **Multi-Tenant `SessionManager`**: Added `SessionManager` and `--max-sessions` concurrency limit to `moonshine serve`, enabling isolated per-connection transcription, event fan-out, agent state, and action dispatching.
- **Transport Session Decoupling**: Updated `WSTransport` and `GRPCTransport` to support per-connection remote sessions and audio ingestion.

---

## [v0.4.1] - 2026-07-24

### Added
- **`--audio-source remote` CLI Flag**: Added `--audio-source` flag to `moonshine serve` for streaming remote PCM audio over WebSocket.
- **`samples/browser-listen`**: Added a zero-install browser sample demonstrating live client-side microphone audio streaming and transcript rendering over WebSocket.

---

## [v0.4.0] - 2026-07-23

### Added
- **`TTSAudioEvent` Wire Events**: Added streaming synthesized audio event frames (`start`, `chunk`, `end`) over transport connections.
- **In-Protocol Barge-In**: Added `session.barge_in` action verb for client-triggered speech interruption.
- **`RemoteAudioSource`**: Introduced `RemoteAudioSource` in `internal/serve` for network-delivered PCM audio ingestion.
- **Runnable Samples**: Added Tier 0/1/2 runnable code examples (`go-listen`, `python-listen`, `python-agent`, `go-cascade-faq`).

---

## [v0.3.0] - 2026-07-23

### Added
- **Importable Daemon Runner**: Extracted `internal/serve.Server` and `ServerConfig` from `cmd/moonshine/serve.go` for in-process sidecar embedding.
- **`--g2p-root` Flag**: Added `--g2p-root` configuration flag for specifying custom Piper/G2P voice model asset directories.

### Changed
- **Upstream Asset Pin**: Updated `MOONSHINE_RELEASE_TAG` to `v0.0.73` (`libmoonshine` with portable Linux glibc support).

---

## [v0.2.1] - 2026-07-23

### Fixed
- **JSON Serialization**: Added `omitempty` struct tags to `TranscriptEvent.Lines` to clean up JSON wire payloads.

---

## [v0.2.0] - 2026-07-23

### Added
- **`pkg/serveapi` Public Extension Surface**: Published zero-cgo leaf package (`pkg/serveapi`) defining Go interfaces (`AgentHandler`, `Retriever`, `LLMClient`, `AudioSource`) and data types (`Line`, `TranscriptEvent`, `ActionRequest`, `ActionResult`, `DisplayCard`).

---

## [v0.1.0] - 2026-07-15

### Added
- **Initial Release**: `moonshine` CLI with model setup, STT transcription, live mic streaming, TTS synthesis, and build versioning.
