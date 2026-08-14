# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [v0.9.2] - 2026-08-14

### Added
- **Runtime Domain Customization (Keyterms & Context Biasing)**: Bound `moonshine_transcriber_set_keyterms` and `moonshine_transcriber_set_context` in `pkg/moonshine` (`Transcriber.SetKeyterms` / `Transcriber.SetContext`), enabling dynamic vocabulary biasing towards jargon, product names, and free-form context text on streaming STT architectures (`#s7du.1`).
- **Domain Customization Action Verbs**: Added `session.set_keyterms` and `session.set_context` action verbs to `moonshine serve` sidecar, allowing external WebSocket/gRPC agents and subscribers to dynamically update vocabulary biasing and context passages mid-session (`#s7du.2`).
- **CLI Flags for Vocabulary Biasing**: Added `--keyterms`, `--keyterm-boost`, `--context`, and `--context-file` flags to `moonshine live`, `moonshine transcribe`, and `moonshine serve` (`#s7du.3`).

### Changed
- **Upstream Library Pin**: Bumped `MOONSHINE_RELEASE_TAG` to `v0.1.2` (`#s7du.5`).

---

## [v0.9.1] - 2026-08-08

### Fixed
- **Speaker Diarization Binding & Option Auto-Wiring**: Bound `moonshine_get_diarization_dependencies` (`GetDiarizationDependencies`), added `DiarizationModelDir()`, auto-wired `diarization_model_dir` option when `--identify-speakers` is enabled, and added diarization model downloading to `moonshine setup --identify-speakers` (`#yp96`).
- **TTS Dependency Manifest Unmarshaling**: Updated `GetTTSDependencyKeys` in `pkg/moonshine/download.go` to parse `v0.1.1` object manifest shape while preserving fallback for legacy flat string array shape, and added `GetTTSDependencies()` manifest getter (`#wllc`).

---

## [v0.9.0] - 2026-08-08

### Added
- **ZipVoice Zero-Shot Voice Cloning**: Added `NewSynthesizerFromClone` in `pkg/moonshine` (binding `moonshine_create_tts_synthesizer_from_memory`) and `--clone <wav_path>` / `--clone-transcript <text>` flags to `moonshine tts` for zero-shot voice cloning using reference WAV audio clips (`#21v`).
- **Clone-Ready Audio Recording**: Added `--record-audio [path_or_dir]` to `moonshine live` (saving microphone capture to a WAV file upon session end with timestamped naming `moonshine_clip_YYYYMMDD-HHMMSS_16k_mono.wav`) and `--save-line-audio <dir>` to `moonshine live` and `moonshine transcribe` (exporting each finalized line's audio + transcript as individual `.wav` and `.txt` files) (`#4ai`).
- **Native WAV Sample-Rate Decoding**: Added `LoadFileWithSampleRate` in `internal/audio` to decode WAV files to mono float32 PCM while preserving native sample rate for ZipVoice cloning inputs (`#21v`).
- **New Runnable Samples**: Added `samples/go-embedded` (in-process STT via `pkg/moonshine`), `samples/mcp-transcribe` (MCP server embedding with MP3 support), and `samples/desktop-app` (Wails v2 desktop application with native file picker and audio decoding) (`#yqp`, `#z1p`, `#2n8`, `#39b`).
- **Sample Verification CI Workflow**: Added `scripts/verify-samples.sh`, `make verify-samples`, and GitHub Actions workflow `.github/workflows/samples-ci.yml` for automated sample compilation & linting (`#9ov`, `#ye4p`, `#76xw`).
- **Pure-Go Build CI Gate**: Added `make check-nocgo` verifying `pkg/moonshine`, `pkg/serveapi`, and `pkg/servepb` build cleanly under `CGO_ENABLED=0` (`#f8k`).
- **Libmoonshine Bundling & Distribution Guide**: Added `docs/bundling-libmoonshine.md` detailing shared library staging, RPATH resolution, and production distribution patterns (`#d9k`).

### Changed
- **Upstream Library Pin & Header Version**: Bumped `MOONSHINE_RELEASE_TAG` pin to `v0.1.1` and `pkg/moonshine.HeaderVersion` to `30000` (`#7sx`).
- **C API Buffer Cleanup**: Switched C-memory buffer deallocation to `moonshine_free_buffer` across bindings to guarantee cross-CRT heap safety (`#2xf`).

### Fixed
- **Dual-Shape JSON Manifest Parsing**: Supported both object-keyed and list-keyed `DependencyGroup` file entries in model manifests returned by `download.moonshine.ai` (`#pn7`).
- **Windows Path Sanitization**: Sanitized scheme colons in `stripScheme` for Windows-compatible `GroupDir` path creation (`#pn7`).
- **macOS Ad-hoc Codesigning**: Added ad-hoc `codesign -s - -f` step for staged `libonnxruntime` shared libraries in `scripts/build-libmoonshine.sh` to prevent AMFI signature kills during dynamic loading.
- **STT Language Code Defaults**: Corrected default STT language code from `en_us` to `en` across samples and CLI error hints (`#3b6`).

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
