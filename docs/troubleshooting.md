# Troubleshooting Guide

Common setup, build, and runtime issues encountered when working with `moonshine-go`, along with diagnostic steps and verified resolutions.

---

## Diagnostic First Step: Run `moonshine doctor`

Whenever a command fails or behavior is unexpected, run `moonshine doctor` first:

```sh
./bin/moonshine doctor
```

`doctor` checks:
- Build prerequisites: Go version, C compiler (for `live`), CMake, and Git LFS
- Dynamic library resolution: `libmoonshine` and `libonnxruntime` presence in `.moonshine/lib` or `MOONSHINE_LIB_DIR`
- STT model cache: downloaded architectures (`tiny`, `tiny-streaming`, `base`)
- TTS voice directory: `tts.g2p_root` presence and voice asset readiness
- GCS credentials (optional): Google Cloud authentication for cloud object transcribing

If any check reports a failure or warning, `doctor` outputs the exact command required to resolve it.

---

## Common Issues & Resolutions

### 1. `libmoonshine not found` or `dlopen failed`

**Symptom:**
```text
Error loading libmoonshine: could not dlopen libmoonshine.dylib: image not found
```

**Cause:**
`moonshine-go` uses pure Go dynamic loading (`ebitengine/purego`) to open `libmoonshine` at runtime. If `MOONSHINE_LIB_DIR` is unset, it searches standard system paths and fails if the library is not installed globally.

**Resolution:**
Point `MOONSHINE_LIB_DIR` at your staged library directory:

```sh
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
```

Verify that both `libmoonshine` and its matching `libonnxruntime` shared library exist in that directory:
- On Linux: `libmoonshine.so` and `libonnxruntime.so.1`
- On macOS: `libmoonshine.dylib` and `libonnxruntime.dylib`

---

### 2. Git LFS Pointer Compiler Errors

**Symptom:**
```text
fatal error: '...': file was recognized as text pointer 'version https://git-lfs.github.com/spec/v1'
```
or `make buildlib` fails with unexpected syntax errors in third-party C++ headers.

**Cause:**
The upstream `moonshine` repository stores several large binaries and embedded models as Git LFS pointers. If the repository was cloned without Git LFS installed, the files remain tiny text pointer stubs instead of real binary assets.

**Resolution:**
Install Git LFS and pull the real file contents in your Moonshine checkout:

```sh
git -C /path/to/moonshine lfs install
git -C /path/to/moonshine lfs pull
```

Then re-run `make buildlib MOONSHINE_SRC=/path/to/moonshine`.

---

### 3. Missing Speech-to-Text Model

**Symptom:**
```text
Error: STT model for en/tiny not found.
Run 'moonshine setup --language en --arch tiny' first.
```

**Cause:**
`moonshine-go` stores model weights in a local user cache (`~/.cache/moonshine_voice` on Linux, `~/Library/Caches/moonshine_voice` on macOS). No models are bundled in the git repository to keep clone sizes small.

**Resolution:**
Download the desired model architecture using `setup`:

```sh
# For batch transcription:
./bin/moonshine setup --arch tiny

# For live streaming transcription:
./bin/moonshine setup --arch tiny-streaming
```

Check all downloaded models with `./bin/moonshine models`.

---

### 4. Cgo Compiler Required for `moonshine live`

**Symptom:**
```text
go build: cgo is required for gen2brain/malgo: C compiler "clang" or "gcc" not found
```

**Cause:**
`moonshine`'s public Go packages (`pkg/moonshine`, `pkg/serveapi`, `pkg/agentflow`) and offline commands (`transcribe`, `setup`, `tts`) build with `CGO_ENABLED=0` and require zero C compilers. However, live microphone capture (`moonshine live`) uses `gen2brain/malgo` to interface with OS audio drivers (CoreAudio, ALSA, WASAPI), which requires cgo.

**Resolution:**
- On macOS: Install Xcode Command Line Tools (`xcode-select --install`).
- On Linux: Install build essentials (`sudo apt-get install build-essential` or equivalent).
- **Alternative (Zero-cgo Live Capture):** If you do not want a C toolchain on your server, run `moonshine serve --audio-source remote` and stream audio over WebSocket from a browser client using **[samples/browser-listen](../samples/browser-listen/)** or **[samples/go-stream-audio](../samples/go-stream-audio/)**.

---

### 5. Getting TTS Voice Models (`tts.g2p_root`)

**Symptom:**
```text
Error: tts.g2p_root not set -- only needed for moonshine tts
```

**Cause:**
Unlike STT models, TTS voice assets (Kokoro and Piper) are stored inside the upstream `moonshine` repository under `core/moonshine-tts/data/`. `moonshine setup` does not download TTS voices automatically yet (tracked in `#wyop`).

**Resolution:**
1. In your upstream `moonshine` checkout, pull only the specific voice assets you need:
   ```sh
   # Pull Kokoro 82M neural voices (~85 MB):
   git -C ~/projects/github/moonshine lfs pull --include="core/moonshine-tts/data/kokoro/**"
   git -C ~/projects/github/moonshine lfs pull --include="core/moonshine-tts/data/en_us/**"

   # Or pull Piper American English Amy Low (~15 MB):
   git -C ~/projects/github/moonshine lfs pull --include="core/moonshine-tts/data/en_us/piper-voices/en_US-amy-low*"
   ```
2. Point `--g2p-root` (or `MOONSHINE_TTS_ROOT`) at the data directory:
   ```sh
   export MOONSHINE_TTS_ROOT="$HOME/projects/github/moonshine/core/moonshine-tts/data"
   ./bin/moonshine tts "Speech synthesis works." --voice kokoro_af_heart -o speech.wav
   ```

---

### 6. Concurrent Multi-Client Streaming VAD Artifacts (Upstream #229)

**Symptom:**
When hosting `moonshine serve --audio-source remote` with multiple live microphone connections speaking at the same time, lines on quieter connections are occasionally dropped or finalized with unexpected delay.

**Cause:**
While `SessionManager` isolates Go streams, event hubs, and dispatchers per connection, the underlying Moonshine C++ engine shares a single static Silero VAD instance across all streams in a process ([moonshine-ai/moonshine#229](https://github.com/moonshine-ai/moonshine/issues/229)). Interleaved audio chunks corrupt recurrent RNN hidden state.

**Resolution:**
For production deployments with multiple simultaneously active live microphones, deploy **one `moonshine serve` container per active talker channel** until upstream isolates VAD state per stream. See **[Hosting & Remote Clients Guide](hosting.md#sessions--one-per-connection-via---max-sessions)**.
