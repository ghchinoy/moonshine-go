# User Guide

Full command/flag reference and worked examples for moonshine-go. See the
top-level [README.md](../README.md) for build/install steps and
[Configuration](../README.md#configuration) for config file / env var
details -- this guide assumes you already have `bin/moonshine` built and
`MOONSHINE_LIB_DIR` (or `--lib-dir`) pointing at a built `libmoonshine`.

## Contents

- [Commands at a glance](#commands-at-a-glance)
- [doctor](#doctor)
- [setup](#setup)
- [models](#models)
- [transcribe](#transcribe)
- [live](#live)
- [serve](#serve)
- [tts](#tts)
- [config](#config)
- [Runtime Domain Customization (Keyterms & Context Biasing)](#runtime-domain-customization-keyterms--context-biasing)
- [Speaker diarization and word timestamps](#speaker-diarization-and-word-timestamps)
- [Choosing a model architecture](#choosing-a-model-architecture)
- [Troubleshooting](#troubleshooting)

## Commands at a glance

| Command | Purpose |
|---|---|
| `moonshine doctor` | Check build/runtime prerequisites and suggest fixes |
| `moonshine setup` | Download STT model files for a (language, arch) pair |
| `moonshine models` | List available STT models and local download status |
| `moonshine transcribe <file\|gs://...>` | Transcribe one audio file, start to finish |
| `moonshine live` | Transcribe continuously from the microphone |
| `moonshine serve` | Start the agentic voice sidecar daemon (WebSocket + gRPC IPC) |
| `moonshine tts <text>` | Synthesize speech to a WAV file |
| `moonshine config` | List or set persistent config.yaml values |
| `moonshine version` (or `--version`) | Print the CLI's own build version (see `doctor` for libmoonshine's version) |

Every command also accepts the global flags `--json`, `--lib-dir`, and
`--model-dir` (see the README's Configuration table).

## doctor

Checks the tools/files moonshine-go needs, instead of finding out via a
failed command's error message. Run it any time something isn't working, or
right after `make buildlib`/`make build` to confirm everything's in place:

```sh
moonshine doctor
moonshine doctor --language en --arch base   # check a specific model pair
moonshine --json doctor                      # for scripting
```

```
$ moonshine doctor
moonshine doctor
--------------------------------------------------
  [ OK ] cmake                              cmake version 4.3.4
  [ OK ] C++ compiler                       c++ -- Apple clang version 21.0.0
  [ OK ] git-lfs                            git-lfs/3.7.1 (GitHub; darwin arm64; go 1.25.3)
  [ OK ] Go toolchain                       go version go1.25.8 darwin/arm64
  [ OK ] cgo (for 'live')                   CGO_ENABLED=1
  [ OK ] libmoonshine                       loaded .moonshine/lib/libmoonshine.dylib (version 20000)
  [ OK ] STT model (transcribe)             en/tiny: 4 file(s) at ~/Library/Caches/moonshine_voice/...
  [ OK ] STT model (live)                   en/tiny-streaming: 8 file(s) at ~/Library/Caches/moonshine_voice/...
  [ OK ] GCS credentials (for gs:// input)  GOOGLE_APPLICATION_CREDENTIALS=...
  [SKIP] TTS voice assets (--g2p-root)      tts.g2p_root not set -- only needed for `moonshine tts`
--------------------------------------------------
summary: 9 ok, 0 warn, 0 fail, 1 skip
```

Two tiers of checks:

| Tier | Checks | Notes |
|---|---|---|
| Build-time | `cmake`, a C++ compiler, `git-lfs`, a Go toolchain, `CGO_ENABLED` | What `make buildlib`/`make build` need. `CGO_ENABLED` only matters for `live` (mic capture) -- `transcribe`/`setup`/`tts` don't need cgo. |
| Runtime | `libmoonshine` resolves and dlopens, an STT model exists for `--language`/`--arch` (`stt.arch`) *and* separately for `live.arch` if it differs, GCS Application Default Credentials (best-effort), `tts.g2p_root` (best-effort) | `STT model (live)` only appears as a separate row when `live.arch` != the checked `--arch` -- see [config](#config) for why they're independent keys. The GCS/TTS checks are only relevant if you use `gs://` input or `tts` -- they report `SKIP`, not `FAIL`, when unset, since they're optional. |

`[FAIL]` on any check makes `moonshine doctor` exit non-zero (useful in
scripts/CI); `[WARN]`/`[SKIP]` don't. `--json` gives the same checks as a
`{"checks": [{"name", "status", "detail"}, ...]}` array instead of a table.

## setup

```sh
moonshine setup                                    # tiny, en (defaults)
moonshine setup --arch base --language en
moonshine setup --arch tiny-streaming --language en # for `live`
moonshine setup --force                             # re-download even if present
```

| Flag | Default | Notes |
|---|---|---|
| `--language` | `en` | Language code or English name, e.g. `en`, `Spanish` |
| `--arch` | `tiny` | `tiny`, `base`, `tiny-streaming`, `base-streaming`, `small-streaming`, `medium-streaming` |
| `--force` | `false` | Re-download even if files already exist |

Downloads land under `<model.dir>/<url-path>/`, e.g.
`~/Library/Caches/moonshine_voice/download.moonshine.ai/model/tiny-en/quantized/tiny-en/`
on macOS -- see the README's Configuration section for why it's namespaced
this way (short version: different archs/languages share file names, so a
flat directory would silently corrupt a previously-downloaded model).

`transcribe` and `live` need a model downloaded for the exact
`(--language, --arch)` pair you run them with -- if you switch `--arch`,
run `setup` again for that arch first.

**`setup` only ever resolves `stt.arch`, never `live.arch`** (see
[live](#live) and [config](#config) below for why they're separate keys) --
it has no concept of "the arch `live` will use." Since their defaults
already differ (`tiny` vs `tiny-streaming`), a fresh install almost always
needs two `setup` runs, one per command:

```sh
moonshine setup --arch tiny             # for transcribe
moonshine setup --arch tiny-streaming   # for live
```

`moonshine doctor` checks both model directories (as separate rows) so a
missing one shows up before you hit the error mid-command.

## models

Lists all supported speech-to-text model architectures (streaming vs non-streaming) and checks whether each model is currently downloaded in the local model directory (`--model-dir`).

```sh
moonshine models
moonshine models --language en
moonshine --json models
```

```
$ moonshine models
moonshine models (language: en)
model.dir: /Users/username/Library/Caches/moonshine_voice
--------------------------------------------------
  tiny             non-streaming [downloaded] /Users/.../tiny-en (4 files)
    Fast, lightweight non-streaming model for offline transcription
  base             non-streaming [downloaded] /Users/.../base-en (4 files)
    Larger non-streaming model with higher accuracy
  tiny-streaming   streaming     [downloaded] /Users/.../tiny-streaming-en (8 files)
    Low-latency streaming model for live speech
  base-streaming   streaming     [not cached]
    Base-size streaming model (defined in C API; not currently published in English CDN catalog)
  small-streaming  streaming     [downloaded] /Users/.../small-streaming-en (8 files)
    Small streaming model for complex audio
  medium-streaming streaming     [downloaded] /Users/.../medium-streaming-en (8 files)
    Largest streaming model for maximum accuracy
--------------------------------------------------
```

| Flag | Default | Notes |
|---|---|---|
| `--language` | `en` | Language code or English name to check model files for |

## transcribe

```sh
# Local file, default arch (tiny) and language (en).
moonshine transcribe recording.wav

# A GCS object -- downloaded to a temp dir first via application default
# credentials (gcloud auth application-default login, or a service account
# via GOOGLE_APPLICATION_CREDENTIALS).
moonshine transcribe gs://my-bucket/recording.wav

# Pick a different model.
moonshine transcribe --arch base --language en recording.wav

# Save the transcript to a file too (plain text, or JSON if --json is set).
moonshine transcribe -o transcript.txt recording.wav

# Machine-readable output (stats + lines, audio arrays stripped by default).
moonshine --json transcribe recording.wav > result.json
moonshine --json transcribe --with-audio recording.wav > result-with-audio.json
```

| Flag | Default | Notes |
|---|---|---|
| `--language` | `en` | Must match a language you already ran `setup` for |
| `--arch` | `tiny` | See [Choosing a model architecture](#choosing-a-model-architecture) |
| `--providers` | `""` (CPU-only) | See [docs/hardware-acceleration.md](hardware-acceleration.md) before changing |
| `-o, --output` | (none) | Also write the transcript to this file |
| `--keyterms` | `""` | Comma-separated key terms to bias speech recognition towards (e.g. `Kubernetes,Ceph,etcd`; streaming models only) |
| `--keyterm-boost` | `2.0` | Keyterm biasing boost strength (default: 2.0; streaming models only) |
| `--context` | `""` | Passage of free-form text from which key terms are automatically extracted (streaming models only) |
| `--context-file` | `""` | Path to text file containing free-form context for keyterm extraction (streaming models only) |
| `--with-audio` | `false` | Include each line's raw per-line audio samples in `--json` output |
| `--identify-speakers` | `false` | Enable speaker diarization -- see [Speaker diarization and word timestamps](#speaker-diarization-and-word-timestamps) below |
| `--word-timestamps` | `false` | Enable per-word timing -- see [Speaker diarization and word timestamps](#speaker-diarization-and-word-timestamps) below |
| `--save-line-audio` | (none) | Save each transcribed line's raw audio and transcript text as individual `.wav`/`.txt` files under this directory (see [Recording and cloning a voice](#recording-and-cloning-a-voice)) |
| `-c, --concurrency` | `1` | Worker pool concurrency for batch mode (reuses single ONNX model) |
| `-r, --recursive` | `true` | Recursively scan subdirectories for `.wav` files in batch mode |
| `--json` (global) | `false` | Machine-readable output on stdout instead of styled text |

Currently only `.wav` input is decoded directly. For other formats, convert
first:

```sh
ffmpeg -i input.mp3 -ar 16000 -ac 1 input.wav
moonshine transcribe input.wav
```

**What you'll see**: an animated spinner per stage (decoding, loading the
model, transcribing) in an interactive terminal, replaced by plain
`stage...` lines when stdout/stderr isn't a terminal (piped, `--json`, or
`NO_COLOR` set) -- so scripted output stays clean. Example, against a
public-domain test clip:

```
$ moonshine transcribe two_cities.wav
[  0.99s] It was the best of times, it was the worst of times.
[  4.80s] It was the age of wisdom,
[  6.43s] It was the age of foolishness.
[  8.58s] It was the epoch of belief.
[ 10.56s] It was the epoch of incredulity.
[ 13.22s] It was a season of light.
[ 14.91s] It was a season of darkness.
[ 17.31s] It was the swin of hope. It was the winter of despair.
[ 20.99s] We had everything before us, we had nothing before us.
--------------------------------------------------
stats: load=182ms decode=243ms infer=655ms audio=44.37s rtf=67.7x
```

`stats` (also in `--json` output as `model_load_ms`/`decode_ms`/
`inference_ms`/`real_time_factor`) tells you how much of the wall-clock time
was model loading vs decoding vs actual inference, and how many times faster
than real-time the transcription ran (`rtf`). See
[docs/faq.md](faq.md#does-transcription-run-at-1x-speed-real-time-or-fasterslower)
for what affects that number.

### Multi-file & Batch Transcription

`moonshine transcribe` accepts multiple positional files, directories, file globs, or GCS URIs/prefixes. When given multiple inputs, it operates in **batch mode**: the ONNX model is loaded **once** and reused across all inputs, bounded by worker pool concurrency (`--concurrency` / `-c`).

```sh
# Transcribe multiple files in one batch
moonshine transcribe audio1.wav audio2.wav audio3.wav

# Transcribe all .wav files in a directory (recursive by default)
moonshine transcribe audio_dir/

# Transcribe matching file globs
moonshine transcribe "recordings/*.wav"

# Transcribe a GCS folder prefix
moonshine transcribe gs://my-bucket/audio/

# Concurrency tuning (worker pool size)
moonshine transcribe -c 4 audio_dir/

# Export structured batch manifest JSON
moonshine --json transcribe audio_dir/ > batch_manifest.json
```

#### Batch Manifest JSON Schema (`--json`)

When running in batch mode with `--json`, `stdout` outputs a structured **Batch Manifest** containing aggregate statistics and per-file results:

```json
{
  "version": "1.0",
  "summary": {
    "total_files": 3,
    "successful_files": 3,
    "failed_files": 0,
    "total_audio_sec": 45.2,
    "total_inference_ms": 1250.0,
    "aggregate_rtf": 36.16,
    "confidence_summary": {
      "mean": 0.92,
      "min": 0.74,
      "max": 0.98
    }
  },
  "results": [
    {
      "input": "audio_dir/file1.wav",
      "status": "ok",
      "error": "",
      "stats": {
        "download_ms": 0,
        "decode_ms": 12.5,
        "inference_ms": 410.0,
        "audio_duration_sec": 15.0,
        "real_time_factor": 36.58,
        "mean_confidence": 0.94
      },
      "lines": [
        {
          "text": "it was the best of times",
          "start_time": 0.0,
          "duration": 2.5,
          "last_latency_ms": 180,
          "confidence": 0.95,
          "words": [
            { "word": "it", "start_time": 0.0, "end_time": 0.2, "confidence": 0.98 }
          ]
        }
      ]
    }
  ]
}
```

- **Fault Isolation**: If a file fails to decode or transcribe, `status` is set to `"failed"` with the `error` details, and processing continues uninterrupted for the remaining files.
- **Deterministic Ordering**: Output results preserve the exact order inputs were supplied or expanded.

### Example audio: `test-assets/` in the moonshine checkout

Don't have a `.wav` handy to try `transcribe` against? The local moonshine
checkout you already have for `make buildlib` (`$MOONSHINE_SRC` /
`moonshine.src_dir`) ships real speech clips at `test-assets/` -- the same
ones `make smoke`'s `MOONSHINE_SMOKE_WAV` uses:

| File | Sample rate | Duration | Notes |
|---|---|---|---|
| `two_cities_16k.wav` | 16 kHz | 44.4s | *A Tale of Two Cities* opening, LibriVox, public domain -- already 16 kHz, no resampling needed |
| `two_cities.wav` | 48 kHz | 44.4s | Same reading at its original sample rate, to exercise `transcribe`'s internal resampling |
| `two_cities_librivox_48k.wav` | 48 kHz | 56.3s | A different LibriVox take on the same public-domain text |

```sh
moonshine transcribe "$MOONSHINE_SRC/test-assets/two_cities_16k.wav"
```

`test-assets/` also has a couple of other short spoken clips (`beckett.wav`,
`endgame_nagg_nell.wav`) checked in for moonshine's own upstream tests --
useful as quick smoke input, but unlike the `two_cities*` LibriVox clips
above, we haven't independently verified their licensing for reuse beyond
local testing, so treat them as convenient scratch audio rather than
citable example clips.

### Speaker diarization and word timestamps

Both are opt-in, transcriber-creation-time options (moonshine's own
`identify_speakers`/`word_timestamps`) -- off by default because diarization
in particular adds significant compute:

```sh
# Per-word timing: each --json line gets a "words" array, and text output
# gets an indented "word@1.23" summary line under each transcript line.
moonshine transcribe --word-timestamps recording.wav

# Speaker diarization: each --json line gets a "speaker_spans" array, and
# text output is prefixed with a speaker label like [S0]. Automatically
# turns on --word-timestamps too (speaker spans are anchored to word
# boundaries). Note: requires downloading diarization models via setup first.
moonshine setup --identify-speakers
moonshine transcribe --identify-speakers recording.wav
```

```
$ moonshine transcribe --identify-speakers meeting.wav
[  0.10s] [S0] Let's get started.
[  1.63s] [S1] Sounds good, go ahead.
[  3.30s] [S0] So the first item on the agenda is...
```

Speaker labels (`S0`, `S1`, ...) are assigned in order of first appearance
and are only stable within a single `transcribe` run -- there's no identity
tracking across separate files/invocations. A single-speaker clip (like
`test-assets/two_cities_16k.wav` above) will just show `[S0]` on every line;
diarization is only interesting on audio with more than one speaker.

`live` has the same two flags, plus tuning knobs for how often streaming
diarization re-clusters -- see [live](#live) below.

## live

```sh
# Needs a *-streaming model.
moonshine setup --arch tiny-streaming
moonshine live --arch tiny-streaming

# Plain text output instead of the TUI (for logging/scripting), and save it.
moonshine live --arch tiny-streaming --no-tui -o session.txt

# Poll less often to reduce CPU use on a slower machine (default 250ms).
moonshine live --arch tiny-streaming --poll-interval 500ms
```

| Flag | Default | Notes |
|---|---|---|
| `--language` | `en` | Same rule as `transcribe` |
| `--arch` | `tiny-streaming` | Use a `*-streaming` arch for good latency; non-streaming archs work but poll less efficiently. Config key: `live.arch` -- independent of `transcribe`/`setup`'s `stt.arch`, see [config](#config) |
| `--providers` | `""` (CPU-only) | See [docs/hardware-acceleration.md](hardware-acceleration.md) |
| `--no-tui` | `false` | Plain text output instead of the bubbletea TUI |
| `--poll-interval` | `250ms` | How often to ask the library for an updated transcript |
| `-o, --output` | (none) | Append completed lines to this file as they finalize (works in either display mode) |
| `--keyterms` | `""` | Comma-separated key terms to bias speech recognition towards (e.g. `Kubernetes,Ceph,etcd`; streaming models only) |
| `--keyterm-boost` | `2.0` | Keyterm biasing boost strength (default: 2.0; streaming models only) |
| `--context` | `""` | Passage of free-form text from which key terms are automatically extracted (streaming models only) |
| `--context-file` | `""` | Path to text file containing free-form context for keyterm extraction (streaming models only) |
| `--record-audio` | (none) | Save captured microphone audio to a WAV file (default filename if empty/directory: `moonshine_clip_YYYYMMDD-HHMMSS_16k_mono.wav`) |
| `--save-line-audio` | (none) | Save each finalized line's raw audio and transcript as individual `.wav` and `.txt` files under this directory |
| `--identify-speakers` | `false` | Enable speaker diarization -- lines/TUI get a `[S0]`-style label; see [Speaker diarization and word timestamps](#speaker-diarization-and-word-timestamps) |
| `--word-timestamps` | `false` | Enable per-word timing (`--no-tui` prints a `word@1.23` summary line per completed line) |
| `--diarization-cluster-cadence` | `2.0` (seconds) | Minimum time between diarization re-clustering passes; raise to reduce cost on long sessions (only with `--identify-speakers`) |
| `--diarization-analyze-cadence` | `1.0` (seconds) | Time between diarization segmentation/embedding model runs (only with `--identify-speakers`) |
| `--diarization-cluster-window-sec` | `120.0` (seconds) | How much audio history re-clustering considers each refresh; `0` = unlimited full history (only with `--identify-speakers`) |
| `--line-stats` | `false` | In `--no-tui` mode, print a stderr note per finalized line with its time-to-final and revision count. The TUI always shows this in its footer; the end-of-session summary is always printed either way. |

Streaming diarization re-clusters a sliding window as more speech arrives,
so speaker labels on recently-finalized lines can still change as `live`
gets more context -- unlike text/timing, which are frozen once a line is
complete. The three `--diarization-*` flags trade accuracy/recency for CPU
cost on long-running sessions; the defaults match moonshine's own.

Press `q`, `Esc`, or Ctrl-C to stop -- either way, `live` stops the stream
cleanly and shows final stats (or writes them to stderr in `--no-tui` mode):

```
$ moonshine live --arch tiny-streaming --no-tui --line-stats
loading model...
opening microphone...
listening... (ctrl-c to stop)
It was the best of times, it was the worst of times.
line-stats: ttf=740ms revisions=3 stability=57%
It was the age of wisdom,
line-stats: ttf=410ms revisions=1 stability=83%
^C
stats: ttft=312ms elapsed=8.4s
summary: lines=2 avg_ttf=575ms max_ttf=740ms avg_revisions=2.0 avg_stability=70%
```

`ttft` is time-to-first-token: how long after the stream started before the
first non-empty transcript line appeared (session-level, one-time) -- the
number that matters most for "does this feel responsive."

`ttf` (time-to-final, per line) is different: how long *that specific line*
took from its first partial appearance to finalizing (`IsComplete`), and
`revisions`/`stability` say how much its text changed while still
in-progress before settling (`stability` is `1 - revisions/observations`;
100% means it never changed after first appearing). These only mean
something for streaming (`live`) output -- `transcribe` never produces
partial lines, so there's nothing to measure. `summary` at the end
aggregates `ttf`/`revisions`/`stability` across every line finalized in the
session. Resolution on all of these is bounded by `--poll-interval` -- a
`ttf` near zero means the line appeared and finalized within one poll
cycle, not that it was literally instantaneous.

The TUI (default, no `--no-tui`) shows the same lines with completed ones in
one color and the current in-progress line in another (with a trailing
cursor glyph), plus a stats footer with `ttft`/`elapsed`/`last_poll`, a
`last_line` row with the most recently finalized line's `ttf`/`revisions`/
`stability`, and (once stopped) the same `summary` row as `--no-tui` above --
all updated live as you speak.

If the microphone won't open, macOS will need to grant terminal/mic
permissions the first time -- see
[Troubleshooting](#troubleshooting) below.

## serve

Starts the agentic voice sidecar daemon: listens continuously on the microphone, streams live `TranscriptEvent` JSON over WebSocket (`/ws`) and/or gRPC (`VoiceSidecar.Stream`), and executes inbound actions (`speak` via TTS, `display` cards, `session.pause/resume/stop`, and LLM tool calls via Gemini).

```sh
# Start sidecar with WebSocket transport and Gemini agent
moonshine serve --transport ws --agent gemini --allow-actions

# Enable both WebSocket and gRPC transports
moonshine serve --transport ws,grpc --addr :8765 --grpc-addr :9090

# External agent mode (IPC subscribers handle logic)
moonshine serve --transport ws --agent external
```

| Flag | Default | Notes |
|---|---|---|
| `--addr` | `:8765` | Address for WebSocket transport |
| `--ws-path` | `/ws` | HTTP path for WebSocket transport |
| `--grpc-addr` | `:9090` | Address for gRPC transport |
| `--transport` | `ws` | Comma-separated transports: `ws`, `grpc`, or `ws,grpc` |
| `--agent` | `external` | Agent mode: `external` (IPC subscribers control logic), `gemini` (built-in Gemini LLM agent), or `agentflow` (built-in in-process `pkg/agentflow` DSL agent) |
| `--gemini-model` | `gemini-2.5-flash` | Gemini model ID for `--agent gemini` |
| `--allow-actions` | `false` | Gate enabling mutating actions (`speak`, `session control`, `run_command`) |
| `--tts-voice` | (auto) | Default TTS voice override |
| `--tts-language` | `en_us` | TTS speaker language |
| `--arch` | `tiny-streaming` | STT model architecture (`tiny-streaming`, `small-streaming`, `medium-streaming`) |
| `--language` | `en` | STT model language |
| `--keyterms` | `""` | Initial comma-separated key terms to bias speech recognition towards (streaming models only) |
| `--keyterm-boost` | `2.0` | Keyterm biasing boost strength (default: 2.0; streaming models only) |
| `--context` | `""` | Initial free-form text passage for automatic keyterm extraction (streaming models only) |
| `--context-file` | `""` | Path to text file containing initial context for keyterm extraction (streaming models only) |

### Supported Action Verbs

When `--allow-actions` is enabled, connected WebSocket and gRPC subscribers can dispatch action requests to the sidecar:

| Verb | Payload (`args`) | Description |
|---|---|---|
| `speak` | `{"text": "...", "voice": "...", "speed": 1.0}` | Synthesizes speech and plays it locally via TTS |
| `display` | `{"title": "...", "body": "...", "kind": "..."}` | Fans out a DisplayCard event to connected UI subscribers |
| `session.pause` | `null` | Mutes microphone input and pauses transcription |
| `session.resume` | `null` | Unmutes microphone input and resumes transcription |
| `session.stop` | `null` | Stops the running sidecar session |
| `session.set_keyterms` | `{"keyterms": ["Kubernetes", "Ceph"]}` | Replaces contextual biasing key terms dynamically mid-session |
| `session.set_context` | `{"context": "...", "max_terms": 200}` | Submits free-form text for automatic tokenizer term extraction |

### Key Invariants
- **Backpressure & Idempotency:** Interim frames drop under backpressure, but every finalized line (`Line.ID`) is delivered to subscribers/agents exactly once.
- **Barge-in Guard:** Microphone input is automatically muted while TTS is speaking to prevent the sidecar from transcribing its own voice.
- **Action Security:** Mutating actions (`speak`, `session.pause/resume/stop`, `run_command`) require `--allow-actions`. `run_command` is disabled by default.

## tts

Moonshine's TTS isn't one proprietary model -- it's a G2P (text
normalization/phonemization) layer moonshine built itself, sitting in front
of **three separate third-party engines** you pick between with `--voice`:

| Engine | What it actually is | Voice prefix / flags |
|---|---|---|
| **Kokoro** | [hexgrad/Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M), an open-weight 82M-parameter TTS model, exported to ONNX | `kokoro_<id>`, e.g. `kokoro_af_heart` |
| **Piper** | [Piper TTS](https://github.com/rhasspy/piper)'s per-voice ONNX files | `piper_<stem>`, e.g. `piper_en_US-amy-low` |
| **ZipVoice** | Zero-shot voice-cloning engine (built-in VCTK voices or custom reference clips via `--clone`) | `zipvoice_<id>` or `--clone ref.wav` |

None of the three are "moonshine's own" acoustic model -- moonshine's actual
contribution is the shared G2P pipeline that feeds text to whichever one you
pick. See [docs/faq.md](faq.md) if you want the full comparison (including
how this differs from a from-scratch Kokoro implementation).

```sh
# Synthesize to out.wav (default). --g2p-root only needed if you haven't
# set moonshine.src_dir (see Configuration below).
moonshine tts --voice piper_en_US-amy-low "Hello world."
moonshine tts --voice kokoro_af_heart "Hello world."

# List available voices for a language first.
moonshine tts --language en_us --list-voices

# Faster/slower speech, different output path.
moonshine tts --voice kokoro_af_heart --speed 1.2 -o greeting.wav "Hi there."

# Hear it immediately -- still writes out.wav (or -o's path) too.
moonshine tts --voice kokoro_af_heart --play "Hi there."
```

| Flag | Default | Notes |
|---|---|---|
| `--language` | `en_us` | Language / CLI tag (config key: `tts.language`) |
| `--voice` | (auto) | `kokoro_<id>`, `piper_<stem>`, or `zipvoice_<id>` -- see `--list-voices` (config key: `tts.voice`; mutually exclusive with `--clone`) |
| `--clone` | (none) | Clone the voice from a reference `.wav` clip using ZipVoice (mutually exclusive with `--voice`) |
| `--clone-transcript` | (none) | Transcript of the `--clone` reference clip (optional; recommended for optimal voice cloning accuracy) |
| `--speed` | `1.0` | Synthesis speed multiplier (config key: `tts.speed`) |
| `--g2p-root` | derived from `moonshine.src_dir` | Directory laid out like moonshine's `core/moonshine-tts/data` (`kokoro/`, `<lang>/piper-voices/`, ...) (config key: `tts.g2p_root`) |
| `-o, --output` | `out.wav` | Output WAV path |
| `--play` | `false` | Play the synthesized audio through the default output device after writing it (cross-platform: CoreAudio/WASAPI/ALSA+PulseAudio via `malgo`/miniaudio, same backend `live`'s mic capture uses) |
| `--ipa` | `` | Synthesize directly from this IPA phonemes string, skipping G2P (mutually exclusive with `<text>`) |
| `--show-phonemes` | `false` | Print the default G2P IPA phonemes for `<text>` and exit, without synthesizing |
| `--list-voices` | `false` | List known voices for `--language` and exit |

### Overriding G2P's phonemes with `--ipa`

Moonshine's TTS isn't one model reading text directly -- it runs a G2P
(grapheme-to-phoneme) step first, converting your text to an International
Phonetic Alphabet (IPA) string, then synthesizes audio from *that*. The G2P
step is a heuristic, and it can mispronounce things, most often proper nouns.
`--show-phonemes` lets you see what G2P actually produced, and `--ipa` lets
you synthesize from a hand-corrected version instead, skipping G2P entirely
for that call.

A concrete example: G2P reads "Reading, Pennsylvania" as if "Reading" rhymed
with "read" (present tense) --

```sh
$ moonshine tts --voice kokoro_af_heart --show-phonemes "Reading, Pennsylvania is a city."
ɹˈidɪŋ pˌɛnsəlvˈeɪnjə ɪz ə sˈɪti
```

-- but the city is pronounced like "Redding". Hand-correct just that word's
phonemes (`ɹˈidɪŋ` -> `ˈɹɛdɪŋ`) and synthesize from the edited IPA directly:

```sh
moonshine tts --voice kokoro_af_heart --ipa "ˈɹɛdɪŋ pˌɛnsəlvˈeɪnjə ɪz ə sˈɪti" -o reading-fixed.wav
```

The phonemes are normalized to the active voice's phoneme inventory before
synthesis, so passing G2P's own unedited output back through `--ipa` for the
same language/voice produces audio equivalent to not using `--ipa` at all --
only the parts you actually change differ.

### Recording and cloning a voice

`--record-audio` (`live`) and `--save-line-audio` (`live`/`transcribe`) exist
specifically to produce clone-ready inputs for `tts --clone`: a reference
`.wav` clip plus its exact transcript, with no manual trimming or re-typing.

**Option A -- record a short clip live, then clone it:**

```sh
# Speak a sentence or two, then Ctrl-C.
moonshine live --arch tiny-streaming --record-audio my_voice.wav

# Clone from the full session recording. Since --record-audio captures the
# whole mic stream (not per-line), --clone-transcript isn't known
# automatically here -- omit it and moonshine tts will auto-transcribe the
# clip internally before cloning.
moonshine tts --clone my_voice.wav "Hello, this is a cloned voice." -o cloned.wav
```

**Option B -- capture one clean per-line clip with its exact transcript:**

```sh
# Saves line_<id>_<start-ms>ms_16k.wav + a matching .txt per finalized line.
moonshine live --arch tiny-streaming --save-line-audio ./clips

# Pick the best line, then clone using its exact transcript -- higher
# quality than auto-transcribing, since there's no ASR round-trip.
moonshine tts --clone ./clips/line_1_000250ms_16k.wav \
  --clone-transcript "$(cat ./clips/line_1_000250ms_16k.txt)" \
  "Hello, this is a cloned voice." -o cloned.wav
```

`transcribe --save-line-audio <dir>` works the same way against an existing
audio file instead of the live microphone, useful for extracting a clean
reference clip from a pre-recorded interview or voicemail.

### Configuring `--g2p-root` once instead of every time

Unlike STT, `moonshine setup` does **not** download TTS voice assets --
libmoonshine's dependency API only returns canonical asset *keys* for
TTS/G2P, not a URL manifest, because voices are published through a
separate pipeline (Kokoro exports, Piper voice files, ZipVoice reference
clips) rather than one flat CDN layout. So `--g2p-root` needs to point at a
moonshine checkout (after pulling the voice assets you want via Git LFS,
below) every time you run `tts`.

Rather than passing `--g2p-root /path/to/moonshine/core/moonshine-tts/data`
on every invocation, set your moonshine checkout's location once:

```sh
moonshine config set moonshine.src_dir ~/projects/github/moonshine
```

`--g2p-root` then defaults to `<moonshine.src_dir>/core/moonshine-tts/data`
automatically (see [config](#config) below). `MOONSHINE_SRC` -- the same env
var `make buildlib` reads -- works too and takes priority over the config
file; `--g2p-root` itself still overrides both if you pass it explicitly
(e.g. to point at a pruned/custom asset directory that isn't a full
checkout).

### Fetching voice assets via Git LFS

Both Kokoro and Piper ship their model weights inside the moonshine repo
itself as Git LFS objects -- nothing is downloaded from Hugging Face or
elsewhere at runtime, you just need to pull the specific paths you want.

**Piper** (one voice, English):

```sh
git -C ~/projects/github/moonshine lfs pull \
  -I "core/moonshine-tts/data/en_us/piper-voices/en_US-amy-low.onnx,core/moonshine-tts/data/en_us/piper-voices/en_US-amy-low.onnx.json,core/moonshine-tts/data/en_us/g2p-config.json,core/moonshine-tts/data/en_us/dict_filtered_heteronyms.tsv,core/moonshine-tts/data/en_us/oov/model.onnx,core/moonshine-tts/data/en_us/oov/onnx-config.json"
```

(English G2P needs the `g2p-config.json`/`dict_filtered_heteronyms.tsv`/`oov/*`
files in addition to the voice itself; other languages have their own
equivalent set under `core/moonshine-tts/data/<lang>/`.)

**Kokoro** (shared model + config, plus per-voice style files which are
small enough not to be LFS-tracked and are usually already real files in
your checkout):

```sh
git -C ~/projects/github/moonshine lfs pull \
  -I "core/moonshine-tts/data/kokoro/model.onnx,core/moonshine-tts/data/kokoro/config.json"
```

`model.onnx` is ~92MB (the 8-bit quantized [onnx-community/Kokoro-82M-ONNX](https://huggingface.co/onnx-community/Kokoro-82M-ONNX)
export) -- see `core/moonshine-tts/data/kokoro/README.md` in the moonshine
checkout for full provenance and how to rebuild it from source if you ever
need a different quantization.

`--list-voices` output looks like:

```
en_us
  kokoro_af_heart                  found
  kokoro_am_adam                   found
  piper_en_US-amy-low              found
  piper_en_US-ljspeech-medium      missing
  zipvoice_american_female         found
  ...
```

**Caveat**: `found` only checks that a file exists at the expected path --
**not** that it's valid content. An unpulled Git LFS pointer stub (a few
hundred bytes of text) still counts as "found." If synthesis fails with an
error mentioning a "Git LFS pointer stub," that's this -- go pull the actual
file (above), then retry.

## config

```sh
moonshine config list                                   # effective values + where each comes from
moonshine config set moonshine.src_dir ~/projects/github/moonshine
moonshine config set stt.arch base
moonshine config set live.arch base-streaming
moonshine config set tts.voice piper_en_US-amy-low
moonshine config path                                    # print the config.yaml path
```

`moonshine config set <key> <value>` only writes the key(s) you've
explicitly set (plus anything already in the file) -- it never dumps every
current default into `config.yaml`. `moonshine config list` shows each key's
currently effective value and its provenance (`default`, `file`, or
`env:VAR_NAME`); it reflects env vars and the config file, not what a flag
on some *other* command would do.

Known keys (also documented in the README's Configuration table):
`lib.dir`, `model.dir`, `moonshine.src_dir`, `stt.language`, `stt.arch`,
`live.arch`, `tts.language`, `tts.voice`, `tts.speed`, `tts.g2p_root`.

A few of these are worth calling out specifically:

- **`stt.arch`/`stt.language`** are shared between `setup` and `transcribe`
  (set once, both commands default to it). `stt.language` is *also* shared
  with `live`, but `live.arch` is its own separate key -- a shared arch
  default tuned for file transcription (`tiny`) would be a poor fit for
  live latency (`tiny-streaming`), and you may genuinely want `transcribe`
  and `live` on different archs at once. **`setup` only ever reads
  `stt.arch`** -- it has no idea `live.arch` exists, so you still need to
  run `setup --arch <that arch>` separately for whichever arch `live` uses
  if it differs from `stt.arch` (this is why the [setup](#setup) examples
  above show it run twice). `moonshine doctor` checks both model
  directories so a missing one is caught proactively.
- **`moonshine.src_dir`** has no dedicated flag on any command; it exists
  purely to derive `tts.g2p_root`'s default (see [tts](#tts) above) and to
  document where your moonshine checkout lives, matching `MOONSHINE_SRC`,
  the env var `make buildlib` already reads.

## Runtime Domain Customization (Keyterms & Context Biasing)

Moonshine v0.1.2 supports runtime vocabulary biasing to improve accuracy on domain-specific terminology (proper names, medical jargon, cloud infrastructure terms, financial tickers) with zero model retraining and zero latency cost during inference.

Biasing works on all **streaming architectures** (`tiny-streaming`, `small-streaming`, `medium-streaming`).

### 1. Exact Keyterm Biasing (`--keyterms`)

When you know the specific vocabulary terms or phrases in advance, supply them as a comma-separated list:

```sh
moonshine live --arch tiny-streaming --keyterms "Kubernetes,Ceph,etcd,Istio,Kustomize"
moonshine transcribe --arch tiny-streaming --keyterms "Atorvastatin,Lisinopril,Echocardiogram" recording.wav
```

- **Prefix-tree boosting:** Each term is tokenized and rewarded progressively during decoding (`boost * (1 + ln(depth))`). Multi-word phrases (e.g. `"Anushka Sharma"`, `"S&P 500"`) are supported.
- **Tuning boost strength (`--keyterm-boost`):**
  - `0.0`: Biasing disabled.
  - `1.0`: Gentle boost, zero accuracy penalty on general words.
  - `2.0` *(default)*: Recommended balance, cuts ~25% of errors on listed terms with minimal impact on other words.
  - `3.0` – `4.0`: Aggressive boost for curated short lists where domain terms matter most. Avoid values > 4.0.

### 2. Automatic Context Extraction (`--context-file` or `--context`)

When you have reference text (e.g. release notes, meeting agendas, clinical records, or ticket descriptions) but have not enumerated individual terms, pass the document directly. The model's tokenizer extracts and ranks the rarest subwords automatically (capped at 200 terms by default):

```sh
moonshine live --arch tiny-streaming --context-file migration-plan.md
moonshine transcribe --arch tiny-streaming --context-file patient-history.txt clinical-audio.wav
```

### 3. Dynamic Mid-Session Switching in `moonshine serve`

In daemon mode (`moonshine serve`), external agents can update keyterm biasing on the fly as the user navigates across different screens or workflows:

- Dispatch `{"verb": "session.set_keyterms", "args": {"keyterms": ["..."]}}` to replace the active keyterm set.
- Dispatch `{"verb": "session.set_context", "args": {"context": "...", "max_terms": 200}}` to submit new context text.
- Pass `{"keyterms": []}` or `{"context": ""}` to clear biasing.

For a full working example of live voice-triggered domain switching, see **[samples/go-domain-customization](../samples/go-domain-customization/)**.

## Choosing a model architecture

| Arch | Use for | Relative speed (same hardware/clip) |
|---|---|---|
| `tiny` | Fastest, least accurate; good default for `transcribe` | ~65-70x real time (CPU) |
| `base` | More accurate, notably slower | ~42-44x real time (CPU) |
| `tiny-streaming` / `base-streaming` / `small-streaming` / `medium-streaming` | Required for `live`; incremental/low-latency variants of the above | comparable to their non-streaming counterparts per poll |

These numbers are from one measurement session on Apple Silicon CPU-only --
see [docs/hardware-acceleration.md](hardware-acceleration.md) for why CPU
(not CoreML) is the default, and [docs/faq.md](faq.md) for the full RTF
writeup. Re-measure on your own hardware with:

```sh
moonshine --json transcribe --arch <arch> <file>.wav 2>/dev/null \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['stats'])"
```

## Troubleshooting

Run `moonshine doctor` first -- it checks most of what's below in one shot
(build tools, `libmoonshine`, the model directory, GCS credentials) and
prints the exact fix command for whatever's missing. The rest of this
section covers things `doctor` doesn't (or can't) check automatically.

**`compiler errors mentioning git-lfs.github.com` or `unknown type name 'version'` when running `make
buildlib`** -- the moonshine checkout has unpulled LFS files. Run
`git -C <checkout> lfs pull` (or `git lfs install --local` first if you see
"Git LFS is not installed for this repository"). `moonshine doctor` confirms
`git-lfs` itself is installed, but can't tell whether a given checkout's
LFS files have actually been pulled.

**`Model directory does not exist at path ...` / `load_transcriber_from_files:
Unknown error`** -- you haven't run `moonshine setup` for that exact
`(--language, --arch)` combination yet. The error includes the exact
`moonshine setup ...` command to run.

**`live` fails to open the microphone** -- on macOS, grant microphone access
to your terminal app under System Settings > Privacy & Security >
Microphone, then try again.

**CoreML errors, empty transcripts, or `--providers` weirdness** -- see
[docs/hardware-acceleration.md](hardware-acceleration.md); CPU-only is the
default for documented reasons, CoreML is opt-in and has known issues with
some archs on the vendored onnxruntime build.

**TTS: `English G2P: in-memory g2p-config.json is a Git LFS pointer stub`**
(or the same for `model.onnx`/a `.onnx.json` file) -- you pointed
`--g2p-root` at a moonshine checkout where the relevant language's G2P/voice
files haven't been `git lfs pull`-ed yet. See [tts](#tts) above for exactly
which files each engine needs; note that `--list-voices` reporting a voice
as `found` does **not** guarantee its files are pulled -- it only checks
that something exists at the expected path.

**TTS: nothing happens / `--g2p-root` seems ignored** -- if you haven't set
`moonshine.src_dir` (`moonshine config set moonshine.src_dir ...`) or
`$MOONSHINE_SRC`, `tts.g2p_root` has no default and you must pass
`--g2p-root` explicitly every time. Run `moonshine config list` to see what
`tts.g2p_root` is currently resolving to.
