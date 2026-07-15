# moonshine-go

A Go client + CLI for the [Moonshine voice library](https://github.com/moonshine-ai/moonshine)
(STT + TTS), built directly on `libmoonshine`'s C API rather than
reimplementing its model pipeline.

<img width="1152" height="784" alt="Image" src="https://github.com/user-attachments/assets/46d0d6bf-eeb5-4c2b-9ac8-3c3b0bfa3643" />

- `internal/moonshine` -- a pure-Go binding (no cgo needed to *build* it) that
  dlopens `libmoonshine.{dylib,so}` at runtime via
  [`ebitengine/purego`](https://github.com/ebitengine/purego) and calls
  directly into its exported C functions. This is the same integration point
  moonshine's own Python bindings use (`ctypes.CDLL` over `moonshine-c-api.h`).
- `cmd/moonshine` -- a cobra/viper CLI: `setup`, `transcribe`, `live`, `tts`.

This isn't published to any package registry -- it's built from source
against a local moonshine checkout (below), and there's no Go-only `go
install` path since the CLI needs the native `libmoonshine` shared library at
runtime.

## Contents

- [Prerequisites](#prerequisites)
- [Build libmoonshine](#build-libmoonshine)
- [Build and use the CLI](#build-and-use-the-cli)
- [Configuration](#configuration)
- [Verifying the bindings](#verifying-the-bindings)
- [Project layout](#project-layout)
- [Docs](#docs)
- [Contributing](#contributing)

## Prerequisites

- Go 1.25+
- CMake 3.22+ and a C++20 compiler (Xcode Command Line Tools on macOS,
  `build-essential` or equivalent on Linux) -- to build `libmoonshine` itself
- [Git LFS](https://git-lfs.com/) -- the moonshine checkout ships several
  files as LFS pointers (embedded C++ sources, vendored onnxruntime
  binaries, TTS voice assets)
- A C toolchain for `go build` when using `live` (mic capture uses cgo via
  `gen2brain/malgo`); `transcribe`/`setup`/`tts` don't need one

Once you have a built `bin/moonshine`, run `./bin/moonshine doctor` to check
all of the above (plus `libmoonshine`, downloaded models, and GCS
credentials) in one shot, with specific fix commands for anything missing.

## Build libmoonshine

Clone a local checkout of moonshine itself (this is a separate, much larger
repo that moonshine-go builds against, not something vendored in here):

```sh
git clone https://github.com/moonshine-ai/moonshine.git ~/projects/github/moonshine
git -C ~/projects/github/moonshine lfs pull
```

Then build and stage `libmoonshine` + its onnxruntime dependency:

```sh
make buildlib MOONSHINE_SRC=/path/to/moonshine
# equivalently: MOONSHINE_SRC=/path/to/moonshine ./scripts/build-libmoonshine.sh
```

This configures/builds moonshine's `core/` CMake project (target `moonshine`)
and copies the resulting shared library, plus the matching onnxruntime
dylib/so, into `.moonshine/lib/`. Point moonshine-go at it with:

```sh
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
```

(or pass `--lib-dir` to any command). On macOS, `libmoonshine.dylib` is built
with `INSTALL_RPATH=@loader_path`, so onnxruntime must sit next to it --
the build script handles that. On Linux the onnxruntime dependency is
resolved via an rpath pointing back at the original checkout, so don't
delete `$MOONSHINE_SRC` after building.

## Build and use the CLI

```sh
make build   # -> bin/moonshine ; equivalently: go build -o bin/moonshine ./cmd/moonshine

# Check build/runtime prerequisites (build tools, libmoonshine, models,
# GCS credentials) in one shot -- run this any time something isn't working.
./bin/moonshine doctor

# Download STT model assets (tiny/base/*-streaming) into the model cache.
./bin/moonshine setup --arch tiny

# Transcribe a local file or a GCS object.
./bin/moonshine transcribe path/to/audio.wav
./bin/moonshine transcribe gs://my-bucket/audio.wav

# Speaker diarization and/or per-word timing (both opt-in; diarization adds
# significant compute). See docs/user-guide.md for details.
./bin/moonshine transcribe --identify-speakers path/to/audio.wav
./bin/moonshine transcribe --word-timestamps path/to/audio.wav

# Live microphone transcription with a bubbletea TUI (Ctrl-C / q to stop).
# Use a *-streaming arch for good latency.
./bin/moonshine setup --arch tiny-streaming
./bin/moonshine live --arch tiny-streaming
./bin/moonshine live --no-tui   # plain text, for scripting/logging

# -o/--output saves the transcript to a file in addition to stdout/the TUI.
./bin/moonshine transcribe -o transcript.txt path/to/audio.wav
./bin/moonshine live -o session.txt

# Text to speech. TTS voice assets (Kokoro/Piper/ZipVoice) aren't
# auto-downloaded by `setup` (see `moonshine tts --help` for why) --
# point --g2p-root at a moonshine checkout's core/moonshine-tts/data,
# after `git lfs pull`-ing the voice(s) you want.
./bin/moonshine tts --g2p-root /path/to/moonshine/core/moonshine-tts/data \
  --language en_us --voice piper_en_US-amy-low -o out.wav "Hello world."
./bin/moonshine tts --g2p-root ... --list-voices
```

**Example output** (`transcribe` against a public-domain test clip):

```
[  0.99s] It was the best of times, it was the worst of times.
[  4.80s] It was the age of wisdom,
[  6.43s] It was the age of foolishness.
[  8.58s] It was the epoch of belief.
[ 10.56s] It was the epoch of incredulity.
[ 13.22s] It was a season of light.
[ 14.91s] It was a season of darkness.
--------------------------------------------------
stats: load=80ms decode=235ms infer=619ms audio=44.37s rtf=71.7x
```

`transcribe` shows an animated progress spinner per stage while running in an
interactive terminal (decode/load/inference); `live` renders a bubbletea TUI
with interim (in-progress) lines styled differently from finalized ones, plus
a stats footer (time-to-first-token, elapsed, last poll latency).

See **[docs/user-guide.md](docs/user-guide.md)** for the full command/flag
reference, more examples (GCS input, `--json`, saving output, choosing an
architecture), and troubleshooting.

`live` requires cgo (microphone capture uses
[`gen2brain/malgo`](https://github.com/gen2brain/malgo), a miniaudio
wrapper) -- build with `CGO_ENABLED=1` (the Go default when a C toolchain is
present). The `internal/moonshine` bindings themselves remain cgo-free.

Build output goes to `./bin` (gitignored); native library output goes to
`./.moonshine` (also gitignored) -- see `make clean` / `make distclean`.

Both commands also accept `--providers` to opt into ONNX Runtime hardware
acceleration (e.g. `CoreML,CPU` on macOS) -- it defaults to CPU-only for a
reason, see [docs/hardware-acceleration.md](docs/hardware-acceleration.md)
before turning it on.

## Configuration

Every command reads config in this priority order: **CLI flag > env var >
`config.yaml` > built-in default**. Manage `config.yaml` with the `config`
command instead of hand-editing it if you prefer:

```sh
moonshine config list                      # effective values + where each comes from
moonshine config set moonshine.src_dir ~/projects/github/moonshine
moonshine config path                      # print the config.yaml path
```

| Config key         | Flag                        | Env var                                        | Default |
|---------------------|-----------------------------|-------------------------------------------------|---------|
| `lib.dir`           | `--lib-dir`                 | `MOONSHINE_LIB_DIR`                              | `./.moonshine/lib` |
| `model.dir`         | `--model-dir`                | `MOONSHINE_MODEL_DIR`, `MOONSHINE_VOICE_CACHE`   | platform cache dir (below) |
| `moonshine.src_dir` | -- (see note below)          | `MOONSHINE_SRC`                                  | (unset) |
| `output.json`       | `--json`                     | --                                                | `false` |
| `stt.arch`          | `--arch` (setup, transcribe) | --                                                | `tiny` |
| `stt.language`      | `--language` (setup, transcribe, live) | --                                     | `en` |
| `tts.language`      | `--language` (tts)           | --                                                | `en_us` |
| `tts.voice`         | `--voice` (tts)              | --                                                | (unset -> auto) |
| `tts.speed`         | `--speed` (tts)              | --                                                | (unset -> 1.0) |
| `tts.g2p_root`      | `--g2p-root` (tts)           | --                                                | derived from `moonshine.src_dir` |

`stt.arch`/`stt.language` are shared between `setup` and `transcribe` (set
one config value, both commands pick it up) -- `live` keeps its own
`tiny-streaming`-oriented default rather than sharing `stt.arch`, since a
default tuned for file transcription would be a poor fit for live latency.

Config file location: `$XDG_CONFIG_HOME/moonshine/config.yaml`, falling back
to `~/.config/moonshine/config.yaml`. Example:

```yaml
lib:
  dir: /Users/you/projects/moonshine-go/.moonshine/lib
model:
  dir: /Users/you/Library/Caches/moonshine_voice
moonshine:
  src_dir: /Users/you/projects/github/moonshine
stt:
  arch: tiny
  language: en
tts:
  language: en_us
  voice: piper_en_US-amy-low
```

**`model.dir` default and interoperability with the Python package**:
`moonshine setup`/`transcribe`/`live` default to the OS-conventional cache
directory for `moonshine_voice` -- the same app name and env var
(`MOONSHINE_VOICE_CACHE`) [moonshine's Python package](https://github.com/moonshine-ai/moonshine)
itself uses (`user_cache_dir("moonshine_voice")`):

- macOS: `~/Library/Caches/moonshine_voice`
- Linux: `$XDG_CACHE_HOME/moonshine_voice` (falls back to `~/.cache/moonshine_voice`)
- Windows: `%LOCALAPPDATA%\moonshine_voice\Cache`

Downloaded models are namespaced under a subdirectory derived from their
download URL (e.g. `download.moonshine.ai/model/tiny-en/quantized/tiny-en/`)
-- both to avoid filename collisions between different
architectures/languages (they share file names like `encoder_model.ort`),
and so pointing `model.dir` at the same cache root `pip install
moonshine-voice` uses lets both tools share downloaded models without
re-fetching them. Override with `--model-dir` / `MOONSHINE_MODEL_DIR` if you
want a project-local or otherwise separate location.

**`moonshine.src_dir`**: the local moonshine checkout used to build
`libmoonshine` (`make buildlib`'s `MOONSHINE_SRC`) is also the same
checkout `tts --g2p-root` needs to point at (Kokoro/Piper/ZipVoice voice
assets live there, fetched via Git LFS -- see
[docs/user-guide.md](docs/user-guide.md#tts)). Setting `moonshine.src_dir`
once (`moonshine config set moonshine.src_dir /path/to/moonshine`) derives
`tts.g2p_root`'s default automatically, so you don't need `--g2p-root` on
every `tts` invocation. It has no dedicated flag since nothing else at
runtime needs it.

## Verifying the bindings

`internal/moonshine/smoke_test.go` (build tag `moonshinesmoke`) exercises a
real built `libmoonshine` end-to-end: STT round-trip on silence, real-speech
non-streaming + streaming transcription against moonshine's own
`test-assets/two_cities_16k.wav`, and TTS synthesis + voice listing.

```sh
make smoke   # basic round-trip only

# Full coverage (real speech + TTS):
MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib" \
MOONSHINE_SMOKE_WAV=/path/to/moonshine/test-assets/two_cities_16k.wav \
MOONSHINE_SMOKE_TTS_ROOT=/path/to/moonshine/core/moonshine-tts/data \
go test -tags moonshinesmoke ./internal/moonshine/... -v
```

## Project layout

```
internal/moonshine/   purego bindings over moonshine-c-api.h (STT, TTS, model download manifests)
internal/audio/       WAV decode/resample + mic capture (cgo, via malgo)
internal/gcsfetch/    gs:// URI download for `transcribe`
internal/session/     live streaming session orchestration (TTFT/latency stats)
internal/tui/         bubbletea/lipgloss live transcription UI
cmd/moonshine/        cobra/viper CLI
scripts/              native build tooling for libmoonshine
docs/                 findings, best practices, FAQ (see below)
skills/               Agent Skills (agentskills.io) for coding agents (see below)
Makefile              buildlib / build / test / smoke / clean targets
```

## Docs

[docs/](docs/) has the full command reference plus findings and answers that
came up building/using this that didn't fit neatly into flag `--help` text:

- [docs/user-guide.md](docs/user-guide.md) -- full walkthrough of every
  command and flag, with real examples and a troubleshooting section.
- [docs/faq.md](docs/faq.md) -- timestamps, transcription speed (RTF),
  progress indicators, model caching, saving output.
- [docs/hardware-acceleration.md](docs/hardware-acceleration.md) --
  `--providers`/CoreML measurements and why CPU is the default.

See [AGENTS.md](AGENTS.md) for issue tracking (bd/beads) and other
agent-facing operational notes.

## Skills

[skills/moonshine-transcribe/](skills/moonshine-transcribe/) is an
[Agent Skills](https://agentskills.io) skill that teaches shell-capable
coding agents (OpenCode, Claude Code, etc.) to drive `moonshine transcribe`
directly -- prerequisite checks via `moonshine doctor`, `--json` parsing,
`gs://` input, diarization/word-timestamp flags -- instead of reimplementing
STT or guessing at flags.

Install it into your agent of choice with the
[Open Agent Skills CLI](https://github.com/vercel-labs/skills) (`npx
skills`), which supports OpenCode, Claude Code, Cursor, and 70+ other
agents:

```sh
# See what's available without installing anything.
npx skills add ghchinoy/moonshine-go --list

# Install into the current project (./<agent>/skills/, committable to git).
npx skills add ghchinoy/moonshine-go --skill moonshine-transcribe

# Install globally instead, so it's available in every project.
npx skills add ghchinoy/moonshine-go --skill moonshine-transcribe -g

# Try it without installing -- pipes a generated prompt straight into an agent.
npx skills use ghchinoy/moonshine-go@moonshine-transcribe | claude
```

## Contributing

This started as a personal/exploratory project, so there's no formal
process yet -- issues and pull requests are welcome. Before sending a PR,
please run:

```sh
make fmt   # gofmt -l . -- should print nothing
make vet   # go vet ./...
make test  # go test ./... (no native deps required)
make smoke # exercises a real built libmoonshine, if you have one staged
```

Licensed under the [Apache License 2.0](LICENSE).
