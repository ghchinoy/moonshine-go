# User Guide

Full command/flag reference and worked examples for moonshine-go. See the
top-level [README.md](../README.md) for build/install steps and
[Configuration](../README.md#configuration) for config file / env var
details -- this guide assumes you already have `bin/moonshine` built and
`MOONSHINE_LIB_DIR` (or `--lib-dir`) pointing at a built `libmoonshine`.

## Contents

- [Commands at a glance](#commands-at-a-glance)
- [setup](#setup)
- [transcribe](#transcribe)
- [live](#live)
- [tts](#tts)
- [config](#config)
- [Choosing a model architecture](#choosing-a-model-architecture)
- [Troubleshooting](#troubleshooting)

## Commands at a glance

| Command | Purpose |
|---|---|
| `moonshine setup` | Download STT model files for a (language, arch) pair |
| `moonshine transcribe <file\|gs://...>` | Transcribe one audio file, start to finish |
| `moonshine live` | Transcribe continuously from the microphone |
| `moonshine tts <text>` | Synthesize speech to a WAV file |
| `moonshine config` | List or set persistent config.yaml values |

Every command also accepts the global flags `--json`, `--lib-dir`, and
`--model-dir` (see the README's Configuration table).

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
| `--with-audio` | `false` | Include each line's raw per-line audio samples in `--json` output |
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
| `--arch` | `tiny-streaming` | Use a `*-streaming` arch for good latency; non-streaming archs work but poll less efficiently |
| `--providers` | `""` (CPU-only) | See [docs/hardware-acceleration.md](hardware-acceleration.md) |
| `--no-tui` | `false` | Plain text output instead of the bubbletea TUI |
| `--poll-interval` | `250ms` | How often to ask the library for an updated transcript |
| `-o, --output` | (none) | Append completed lines to this file as they finalize (works in either display mode) |

Press `q`, `Esc`, or Ctrl-C to stop -- either way, `live` stops the stream
cleanly and shows final stats (or writes them to stderr in `--no-tui` mode):

```
$ moonshine live --arch tiny-streaming --no-tui
loading model...
opening microphone...
listening... (ctrl-c to stop)
It was the best of times, it was the worst of times.
It was the age of wisdom,
^C
stats: ttft=312ms elapsed=8.4s
```

`ttft` is time-to-first-token: how long after the stream started before the
first non-empty transcript line appeared -- the number that matters most for
"does this feel responsive."

The TUI (default, no `--no-tui`) shows the same lines with completed ones in
one color and the current in-progress line in another (with a trailing
cursor glyph), plus a stats footer with `ttft`/`elapsed`/`last_poll`, updated
live as you speak.

If the microphone won't open, macOS will need to grant terminal/mic
permissions the first time -- see
[Troubleshooting](#troubleshooting) below.

## tts

Moonshine's TTS isn't one proprietary model -- it's a G2P (text
normalization/phonemization) layer moonshine built itself, sitting in front
of **three separate third-party engines** you pick between with `--voice`:

| Engine | What it actually is | Voice prefix |
|---|---|---|
| **Kokoro** | [hexgrad/Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M), an open-weight 82M-parameter TTS model, exported to ONNX | `kokoro_<id>`, e.g. `kokoro_af_heart` |
| **Piper** | [Piper TTS](https://github.com/rhasspy/piper)'s per-voice ONNX files | `piper_<stem>`, e.g. `piper_en_US-amy-low` |
| **ZipVoice** | A separate zero-shot voice-cloning engine, weights/reference clips compiled into `libmoonshine` itself | `zipvoice_<id>`, e.g. `zipvoice_american_female` |

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
```

| Flag | Default | Notes |
|---|---|---|
| `--language` | `en_us` | Language / CLI tag (config key: `tts.language`) |
| `--voice` | (auto) | `kokoro_<id>`, `piper_<stem>`, or `zipvoice_<id>` -- see `--list-voices` (config key: `tts.voice`) |
| `--speed` | `1.0` | Synthesis speed multiplier (config key: `tts.speed`) |
| `--g2p-root` | derived from `moonshine.src_dir` | Directory laid out like moonshine's `core/moonshine-tts/data` (`kokoro/`, `<lang>/piper-voices/`, ...) (config key: `tts.g2p_root`) |
| `-o, --output` | `out.wav` | Output WAV path |
| `--list-voices` | `false` | List known voices for `--language` and exit |

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
`tts.language`, `tts.voice`, `tts.speed`, `tts.g2p_root`.

Two of these are worth calling out specifically:

- **`stt.arch`/`stt.language`** are shared between `setup` and `transcribe`
  (set once, both commands default to it) -- but *not* `live`, which keeps
  its own `tiny-streaming`-oriented default since a shared default tuned for
  file transcription would be a poor fit for live latency.
- **`moonshine.src_dir`** has no dedicated flag on any command; it exists
  purely to derive `tts.g2p_root`'s default (see [tts](#tts) above) and to
  document where your moonshine checkout lives, matching `MOONSHINE_SRC`,
  the env var `make buildlib` already reads.

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

**`compiler errors mentioning git-lfs.github.com` when running `make
buildlib`** -- the moonshine checkout has unpulled LFS files. Run
`git -C <checkout> lfs pull` (or `git lfs install --local` first if you see
"Git LFS is not installed for this repository").

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
