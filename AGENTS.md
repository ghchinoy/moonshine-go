# AGENTS.md

Operational context for coding agents working in this repo. End-user/CLI
docs live in [README.md](README.md).

## Issue Tracking

This project uses **bd (beads)** for issue tracking.
Run `bd prime` for workflow context, or install hooks (`bd hooks install`) for auto-injection.

**Quick reference:**
- `bd ready` - Find unblocked work
- `bd create "Title" --type task --priority 2` - Create issue
- `bd close <id>` - Complete work
- `bd dolt push` - Push beads to remote

For full workflow details: `bd prime`

No git remote is configured for this repo; beads sync is local-only.

## Building and verifying

```sh
make buildlib MOONSHINE_SRC=~/projects/github/moonshine   # one-time native build
make build                                                 # go build -> bin/moonshine
make test                                                  # go test ./... (no native deps)
make smoke                                                 # exercises a real libmoonshine.dylib/.so
```

`make smoke` additionally honors `MOONSHINE_SMOKE_WAV` (a 16kHz mono wav,
e.g. moonshine's own `test-assets/two_cities_16k.wav`) and
`MOONSHINE_SMOKE_TTS_ROOT` (a `core/moonshine-tts/data`-shaped directory with
at least one Piper voice's `.onnx`/`.onnx.json` pulled via `git lfs pull`) to
run real-speech and TTS smoke tests, not just the always-on silence
round-trip.

A moonshine checkout ships several files as Git LFS pointers that aren't
needed for the normal `moonshine-voice` app but ARE needed to build
`libmoonshine` (embedded C++ sources) and to run it (vendored onnxruntime
binaries, TTS voice assets). If `scripts/build-libmoonshine.sh` fails with
compiler errors mentioning `git-lfs.github.com`, run `git lfs pull` in that
checkout (see README.md's "Build libmoonshine" section for exactly which
paths matter if you want to avoid pulling the entire LFS payload).

## Architecture notes for future changes

- `internal/moonshine` is purego-based (no cgo to *build*) and must stay that
  way -- it's the whole point of this project over reimplementing the model
  pipeline. `internal/audio`'s mic capture (`gen2brain/malgo`) is a
  deliberate, separate exception that does require cgo.
- C struct layouts in `internal/moonshine/ctypes.go` are hand-mirrored from
  `moonshine-c-api.h` with explicit padding. If that header's structs change
  upstream, re-verify offsets (see the throwaway `offsetof`/`sizeof` C
  program used during initial development, not checked in -- rewrite it
  against the new header if needed) before touching `ctypes.go`.
- STT model downloads are namespaced per (language, arch) under
  `GroupDir()`/`PrimaryModelDir()` in `internal/moonshine/download.go`
  precisely because different models share filenames
  (`encoder_model.ort`, etc.) -- don't "simplify" this back to a flat
  directory.
