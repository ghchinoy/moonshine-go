# FAQ

## Where do the per-line timestamps come from? Is that our code?

All of it comes from moonshine's own C++ core, not moonshine-go.
`internal/moonshine/stt.go`'s `copyTranscript` copies `start_time` and
`duration` straight out of the C library's `transcript_line_t` struct (see
`moonshine-c-api.h`) with no computation on our end. Those values come from
moonshine's internal VAD-based speech segmentation, which splits audio into
"lines" (roughly sentences/phrases) and timestamps each one as part of
transcription itself.

There's also a `word_timestamps` option in the C API for per-word timing
(distinct from per-line timing), which the Go binding models via
`Line.Words` -- exposed as `--word-timestamps` on both `transcribe` and
`live` (see [docs/user-guide.md](user-guide.md#speaker-diarization-and-word-timestamps)).
The same applies to speaker diarization (`--identify-speakers`,
`Line.SpeakerSpans`).

## Does transcription run at 1x speed (real time), or faster/slower?

Non-streaming `transcribe` does a single batch forward pass over the whole
audio array -- it's pure compute-bound inference with no artificial
real-time pacing, so it runs as fast as the model + hardware allow. On this
project's dev machine (Apple Silicon, CPU-only, `tiny` arch), that's
consistently **~50-70x real time** (real-time factor, or RTF = audio
duration / inference time) regardless of clip length:

| Clip length | Inference time | RTF |
|---|---|---|
| 5s | 92ms | 54x |
| 15s | 291ms | 52x |
| 44.4s | 840ms | 53x |

RTF stays roughly constant as duration changes (confirming it scales close
to linearly with audio length -- there's no fixed "wait for it to play"
step), but varies a lot by **model architecture**: `base` was measurably
slower per second of audio than `tiny` on the same clip (RTF ~42x vs ~65x
CPU-only). See [hardware-acceleration.md](hardware-acceleration.md) for how
execution provider choice affects this too.

For `live`, audio still arrives from the mic at real 1x speed (that part's
fixed by the physical world), but each poll's `Transcribe()` call on the
buffered audio is the same fast batch-style operation -- that's exactly why
low time-to-first-token (TTFT) is achievable: the model isn't the
bottleneck, mic buffering/poll interval is.

## Why doesn't `transcribe` show any progress while it's running?

It does now (see `cmd/moonshine/progress.go`) -- if this looks stale, update.
In an interactive terminal, each stage (download, decode, model load,
inference) shows an animated spinner with elapsed time, overwritten in
place. When stdout/stderr isn't a terminal (piped, redirected, `--json`, or
`NO_COLOR` set), it prints one plain status line per stage instead, so
scripted/logged output stays clean. Longer files or slower model
architectures are exactly when this matters most -- see the RTF numbers
above for how long a given file/arch combination might realistically take.

## Where are downloaded models cached, and can I share them with the Python package?

Yes, by default. See the README's Configuration section for the full
picture, but briefly: `model.dir` defaults to the same OS cache-directory
convention (and even the same `MOONSHINE_VOICE_CACHE` env var name) that
`pip install moonshine-voice` uses, and downloaded models are namespaced by
their download URL path under that root -- the same layout the Python
package uses internally. Point both tools at the same cache root and models
download once.

## Why do I need to run `moonshine setup` twice (once per arch)?

Because `setup` downloads exactly one `(language, arch)` pair per
invocation, and `transcribe`/`live` default to *different* archs on
purpose: `transcribe` uses `stt.arch` (default `tiny`, tuned for batch file
accuracy), `live` uses its own `live.arch` (default `tiny-streaming`, tuned
for low-latency polling). `setup` has no idea `live.arch` exists -- it only
ever reads `stt.arch` -- so running it once doesn't cover both commands
unless you happen to set them to the same arch.

If you use both commands, run `setup` once per arch you actually use:

```sh
moonshine setup --arch tiny                # for transcribe
moonshine setup --arch tiny-streaming      # for live
moonshine setup --identify-speakers        # for speaker diarization (segmentation & embedding models)
```

`moonshine doctor` checks both model directories as separate rows (`STT
model (transcribe)` / `STT model (live)`), so a missing one shows up
proactively instead of as a mid-command error. See the README's
Configuration section for why `stt.arch` and `live.arch` are deliberately
separate keys rather than one shared setting.

## Can I save a transcript to a file as well as seeing it on screen?

Yes -- `-o/--output <path>` on both `transcribe` and `live`. It doesn't
replace stdout/the TUI, it writes in addition to it. `transcribe --output`
writes plain `[start] text` lines (or the same JSON as `--json`'s stdout
output, if `--json` is also set). `live --output` appends each completed
line to the file as it finalizes, independent of whether you're using the
TUI or `--no-tui`.

## How should I tune `--concurrency` for batch transcription?

`moonshine transcribe` uses a worker pool when given multiple files, directories, or GCS prefixes (`-c/--concurrency`, default `1`).

Because ONNX Runtime model inference is thread-safe, all workers share a single loaded model instance in memory without reloading model weights per file.

- **CPU execution provider** (default): Setting `--concurrency` up to the number of CPU cores (e.g. `-c 4` or `-c 8`) maximizes batch throughput when transcribing collections of files.
- **CoreML execution provider** (macOS): CoreML hardware dispatch runs best with `--concurrency 1` or `2`. Higher concurrency on CoreML can cause driver queue contention.
- **Memory footprint**: Workers share model parameters in memory (~100MB for `tiny`). Per-worker memory overhead is minimal (only the PCM audio buffer for the active file).

## What languages are supported for STT and TTS, and why does `moonshine setup --language de` fail with `has no model_arch 0 (TINY)`?

Moonshine supports **10 Speech-to-Text (STT) languages** (English, Spanish, German, Mandarin, Japanese, Korean, Vietnamese, Tagalog, Ukrainian, and Arabic) and **16 Text-to-Speech (TTS) languages** (English, Spanish, Arabic, German, French, Hindi, Italian, Japanese, Korean, Dutch, Portuguese, Russian, Turkish, Ukrainian, Vietnamese, and Mandarin).

Because each STT language model is trained at dedicated sizes, **non-English languages support specific `--arch` values** -- and most non-English languages do not ship a `tiny` model (which is `moonshine setup` and `moonshine transcribe`'s default `--arch`). If you run `moonshine setup --language de` without `--arch`, `libmoonshine` reports `language "de" has no model_arch 0 (TINY); supported architectures: 4 (SMALL_STREAMING), 2 (TINY_STREAMING)`.

Always pass a supported `--arch` for your target language:

| Language | Code | Supported STT `--arch` Values | Example `setup` Command |
|---|---|---|---|
| **English** | `en` | `tiny`, `base`, `tiny-streaming`, `small-streaming`, `medium-streaming` | `moonshine setup --language en --arch tiny-streaming` |
| **Spanish** | `es` | `base`, `tiny-streaming`, `small-streaming` | `moonshine setup --language es --arch small-streaming` |
| **German** | `de` | `tiny-streaming`, `small-streaming` | `moonshine setup --language de --arch small-streaming` |
| **Mandarin** | `zh` | `base`, `tiny-streaming` | `moonshine setup --language zh --arch base` |
| **Japanese** | `ja` | `tiny`, `base`, `tiny-streaming`, `small-streaming` | `moonshine setup --language ja --arch small-streaming` |
| **Korean** | `ko` | `tiny` | `moonshine setup --language ko --arch tiny` |
| **Vietnamese** | `vi` | `base`, `tiny-streaming` | `moonshine setup --language vi --arch base` |
| **Tagalog** | `tl` | `tiny-streaming` | `moonshine setup --language tl --arch tiny-streaming` |
| **Ukrainian** | `uk` | `base` | `moonshine setup --language uk --arch base` |
| **Arabic** | `ar` | `base`, `tiny-streaming` | `moonshine setup --language ar --arch base` |

## Why is `Word.Confidence` / `Line.Confidence` always `1.0` (or `0` when `--word-timestamps` is off)?

In `libmoonshine`'s C API (`moonshine-c-api.h`), confidence is a field on `struct transcript_word_t` (`Word.Confidence`); `struct transcript_line_t` has no separate C confidence field. In `pkg/moonshine`, `Line.Confidence` and `Line.MeanConfidence()` are computed in Go by averaging `Line.Words`.

1. **When `--word-timestamps` is off (the default):** `Line.Words` is not populated by the C library, so `Line.Confidence` is `0` (and omitted from JSON output).
2. **When `--word-timestamps` is on:** Upstream `core/word-alignment.cpp` currently hardcodes `tw.confidence = 1.0f` for every aligned word ([moonshine-ai/moonshine#230](https://github.com/moonshine-ai/moonshine/issues/230)). Once upstream routes decoder token probabilities into `align_words()`, `Word.Confidence`, `Line.Confidence`, `Line.MeanConfidence()`, and `Line.MinConfidence()` will automatically surface real per-word and per-line probabilities without any Go API changes.
