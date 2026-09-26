# Quickstart — First Transcript in 5 Minutes

This guide walks you through setting up `moonshine-go`, building the CLI, downloading a speech-to-text model, and transcribing your first audio file.

> **Time expectation:** ~5 minutes on Linux (using prebuilt binaries); ~15–20 minutes on macOS (due to one-time C++ compilation of `libmoonshine` from source).

## 1. Prerequisites

- **Go 1.25+**
- **Git**
- On macOS: Xcode Command Line Tools (`xcode-select --install`) and CMake (`brew install cmake`)
- On Linux: Standard build tools (`build-essential`)

## 2. Clone the Repository & Stage `libmoonshine`

Clone `moonshine-go`:

```sh
git clone https://github.com/ghchinoy/moonshine-go.git
cd moonshine-go
```

`moonshine-go` communicates with the native `libmoonshine` C engine via pure Go bindings. You need `libmoonshine` and its ONNX Runtime dependencies staged into `.moonshine/lib/`:

### Option A: Linux Prebuilt Binaries (Fastest)

On Linux (x86_64 or arm64), fetch precompiled libraries directly from GitHub releases:

```sh
make fetchlib
```

This stages `libmoonshine.so` and `libonnxruntime.so.1` into `.moonshine/lib/`.

### Option B: Build from Source (macOS & Custom Builds)

On macOS or systems without prebuilt release binaries, build `libmoonshine` from the upstream C++ source:

```sh
# Clone moonshine C++ engine (with Git LFS for model runtimes)
git clone https://github.com/moonshine-ai/moonshine.git ~/projects/github/moonshine
git -C ~/projects/github/moonshine lfs pull

# Build and stage libmoonshine into .moonshine/lib:
make buildlib MOONSHINE_SRC=~/projects/github/moonshine
```

### Point your environment to the library:

```sh
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
```

## 3. Build the CLI & Verify Prerequisites

Compile the `moonshine` CLI:

```sh
make build
```

Run `moonshine doctor` to confirm that build tools, dynamic libraries, and model directories are ready:

```sh
./bin/moonshine doctor
```

All essential checks should report green. If any item shows a warning, `doctor` provides the exact fix command.

## 4. Download a Speech-to-Text Model

Download the default lightweight English model (`tiny`, 71 MB on disk):

```sh
./bin/moonshine setup --arch tiny
```

Verify downloaded models:

```sh
./bin/moonshine models
```

## 5. Transcribe Your First Audio File

Download a sample public-domain speech clip (*A Tale of Two Cities*, 16kHz mono WAV):

```sh
curl -sLO https://storage.googleapis.com/moonshine-ports-site-assets/moonshine-go/audio/two_cities_16k.wav
```

Transcribe the audio:

```sh
./bin/moonshine transcribe two_cities_16k.wav
```

Output:

```text
decoding audio...
loading tiny model...
transcribing 44.4s of audio...
[  0.99s] It was the best of times, it was the worst of times.
[  4.80s] It was the age of wisdom,
[  6.43s] It was the age of foolishness.
[  8.58s] It was the epoch of belief.
[ 10.56s] It was the epoch of incredulity.
[ 13.22s] It was a season of light.
[ 14.91s] It was a season of darkness.
[ 17.31s] It was the swin of hope. It was the winter of despair.
[ 20.99s] We had everything before us, we had nothing before us.
[ 24.42s] We all go and direct to heaven,
[ 26.34s] We were all going to act the other way.
[ 28.86s] In short,, the period was so far like the present period,
[ 32.51s] That some of its noisiest authorities insisted on its being received for good or for evil in the superlative degree of comparison only.
--------------------------------------------------
stats: load=146ms decode=234ms infer=671ms audio=44.37s rtf=66.1x
```

## Where to Go Next

Now that you have a working transcription pipeline:

1. **Live Microphone Transcription:**
   Download the low-latency streaming model and start the interactive terminal UI:
   ```sh
   ./bin/moonshine setup --arch tiny-streaming
   ./bin/moonshine live --arch tiny-streaming
   ```
2. **Build an Offline Voice Agent:**
   Read the **[AgentFlow Tutorial](../samples/TUTORIAL.md)** to create a conversational voice bot using Go, or explore the **[Samples Catalog](../samples/)**.
3. **Embed Moonshine in Your Go Applications:**
   Use `pkg/moonshine` directly in your application with zero daemon dependency — see **[samples/go-embedded](../samples/go-embedded/)**.
4. **Text-to-Speech (TTS):**
   Download a voice model directly from the CDN and synthesize speech:
   ```sh
   ./bin/moonshine setup --tts kokoro_af_heart
   ./bin/moonshine tts --voice kokoro_af_heart --play "Hello from Moonshine voice."
   ```
5. **Troubleshooting:**
   If you hit missing library errors or compilation issues, see the **[Troubleshooting Guide](troubleshooting.md)**.
