# Core Agent Persona (`moonshine-go-core`)

Operational context, architectural invariants, and engineering standards for coding agents working on the core library, CLI, and sidecar daemon.

## Scope & Ownership

- **`pkg/moonshine`**: Pure-Go C ABI bindings over `moonshine-c-api.h` (STT, TTS, text embeddings, model manifests).
- **`cmd/moonshine`**: Flagship CLI subcommands (`transcribe`, `live`, `serve`, `tts`, `setup`, `models`, `doctor`, `config`).
- **`internal/`**:
  - `internal/serve`: Agentic voice sidecar daemon (Hub, Dispatcher, transports, agents, session manager).
  - `internal/session`: Streaming session lifecycle, VAD polling, latency tracking.
  - `internal/audio`: Cross-platform audio capture (`malgo`/miniaudio) and playback.
  - `internal/tui`: Terminal UI.
- **Benchmarking & Tooling**: `bench/`, `BENCHMARKS.md`, `Makefile`, and `scripts/`.
- **Releases & Governance**: `CHANGELOG.md`, `docs/RELEASING.md`, `MOONSHINE_RELEASE_TAG`, and upstream synchronization with `~/projects/github/moonshine`.

---

## Upstream Synchronization Routine

When evaluating upstream releases in `~/projects/github/moonshine`:

1. **Check Remote Branches & Tags:**
   Run `git fetch upstream --tags`. Compare your local checkout against `upstream/main` and active `upstream/dev-v*` candidate branches. Ignore benign "would clobber" warnings on historical tags.
2. **Inspect C API Header Diff:**
   ```sh
   git diff <current_tag>..upstream/dev-v* -- core/moonshine-c-api.h
   ```
   If empty, `MOONSHINE_HEADER_VERSION` is unchanged and zero Go binding modifications are required.
3. **Audit Active Issues & PRs:**
   Review active upstream pull requests and issues for known crashes, races, or concurrency limitations (e.g. #223 transcriber destruction race, #229 concurrent Silero VAD state sharing).
4. **Validate Prebuilt Assets:**
   Only bump `MOONSHINE_RELEASE_TAG` on officially tagged releases. Validate archive contents, RPATH, and dynamic library presence using:
   ```sh
   scripts/check-release-asset.sh linux-x86_64 <tag>
   scripts/check-release-asset.sh linux-arm64 <tag>
   ```
5. **Empirical Verification Over Documentation:**
   When upstream capability descriptions seem ambiguous or conflict with observed behavior, probe C API behavior empirically with a minimal script rather than assuming older documentation is accurate.

---

## Release & Semver Cadence

Releases are published intentionally following [docs/RELEASING.md](../docs/RELEASING.md):

- **Semver Minor (`v0.X.0`)**: For new subsystems, major C API capabilities, or backwards-compatible architectural additions (e.g. streaming TTS, text embeddings, domain customization).
- **Semver Patch (`v0.X.Y`)**: For bug fixes, doc corrections, or maintenance updates.
- **Tagging Protocol**: Pushing an annotated Git tag (`git tag -a vX.Y.Z -m "Release vX.Y.Z" && git push origin vX.Y.Z`) triggers `.github/workflows/release.yml`, which packages and publishes Linux x86_64 release archives to GitHub Releases.
- **Changelog Timing**: Write `CHANGELOG.md` entries when preparing the release cut, bundled into the release commit.

---

## C ABI & Purego Binding Architecture

- **No Cgo for Bindings:** `pkg/moonshine` is purego-based (no C compiler or cgo toolchain required to build). It dlopens `libmoonshine` at runtime. `internal/audio`'s mic capture (`gen2brain/malgo`) is a deliberate, isolated exception requiring cgo.
- **Hand-Mirrored Struct Layouts (`ctypes.go`):** Struct layouts in `pkg/moonshine/ctypes.go` (`cTranscriptLine`, `cTTSChunk`, `cOption`, etc.) mirror `moonshine-c-api.h` exactly with explicit padding on 64-bit architectures. Re-verify offsets against C `offsetof`/`sizeof` before altering struct fields.
- **Namespaced Model Storage:** Model downloads are namespaced under `GroupDir(root, group)` in `pkg/moonshine/download.go` (`<model.dir>/<url-path>/`) because different models share identical file stems (`encoder_model.ort`, `decoder_model_merged.ort`). Never flatten the directory layout.
- **Vector Deallocation Convention:** Memory allocated by `moonshine_calculate_embedding` must be freed with `moonshine_free_embedding` (C `std::free`), NOT `moonshine_free_buffer`.

---

## Sidecar Daemon & Runtime Invariants (`internal/serve`)

- **Hub / Dispatcher / Transport Separation:**
  - `Hub`: Live transcript fan-out (`session.Update` -> `TranscriptEvent`). Uses drop-oldest for interim updates, but guarantees every finalized line (`Line.ID`) is delivered to subscribers exactly once.
  - `Dispatcher`: Inbound action routing (`speak`, `display`, `session.pause/resume/stop`, `session.set_keyterms`, `session.set_context`, `run_command`).
  - `Transport`: Merges WebSocket (`ws.go`) and gRPC (`grpc.go` / `serve.proto`) connections.
- **TTS G2P Root Precedence (`resolveG2PRoot()`):** Never read `viper.GetString("tts.g2p_root")` directly. Always call `resolveG2PRoot()` in `cmd/moonshine/lib.go`. It enforces 4-tier precedence:
  1. Explicit CLI flag: `--g2p-root`
  2. Explicit configuration / env: `tts.g2p_root` in `config.yaml`
  3. Upstream source checkout: `<moonshine.src_dir>/core/moonshine-tts/data`
  4. Downloaded cache fallback: `<model.dir>/download.moonshine.ai/tts` (from `moonshine setup --tts`)
  *Note: `setup --tts` intentionally never mutates `config.yaml` so higher-precedence overrides remain active.*
- **Streaming TTS Barge-In State (`TTSStream.Cancel()`):** Calling `stream.Cancel()` aborts active C++ computation, and the immediate subsequent call to `stream.NextChunk()` returns sentinel `ErrCancelled` once. This error must be consumed before pushing a new utterance to reset the synthesizer to idle.
- **Concurrent Stream VAD Limitation (Upstream #229):** All transcription streams in a process share a single static `SileroVad*` pointer in C++ (`VoiceActivityDetector::silero_vad`). Because Silero VAD carries recurrent RNN state (`_state`, `_context`), simultaneous live streams in a single `moonshine serve` process will corrupt each other's speech detection boundaries. For multi-channel live audio, deploy separate `moonshine serve` worker processes (process-level isolation).

---

## Testing & Verification Standards

- **Native-Free Unit Tests:** All core logic in `internal/serve`, `internal/session`, and `pkg/moonshine` must be unit-testable with fakes (`fakeLLMClient`, `fakeTransport`, `fakeSpeaker`) without requiring `libmoonshine` or network access (`make test` must pass in native-free environments).
- **Assert Real Failure Symptoms in Concurrency Tests:** When authoring concurrency tests, do not only assert that transcripts don't interleave words; assert line counts and finalization latency against a solo baseline (e.g. `TestConcurrentStreamVADCorruption`). Upstream bugs often manifest as dropped lines or latency inflation rather than word corruption.
- **`make bench` Excludes Expected Regression Probes:** `make bench` passes `-run '^$'` to run exclusively `Benchmark*` functions and skip regression probe tests that intentionally fail on current upstream library pins until upstream fixes land.
- **Smoke Test Opt-In Flags:**
  - `MOONSHINE_SMOKE_WAV`: 16kHz mono WAV for real speech tests.
  - `MOONSHINE_SMOKE_TTS_ROOT`: Voice data root for TTS synthesis & streaming tests.
  - `MOONSHINE_SMOKE_EMBEDDING=1`: Opt-in flag to run the Gemma-300M embedding download, inference, and semantic matching smoke tests (~300MB download on first run).
