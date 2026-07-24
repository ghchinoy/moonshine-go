# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased] - v0.6.0

### Added
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
