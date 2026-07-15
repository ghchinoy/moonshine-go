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

## Can I save a transcript to a file as well as seeing it on screen?

Yes -- `-o/--output <path>` on both `transcribe` and `live`. It doesn't
replace stdout/the TUI, it writes in addition to it. `transcribe --output`
writes plain `[start] text` lines (or the same JSON as `--json`'s stdout
output, if `--json` is also set). `live --output` appends each completed
line to the file as it finalizes, independent of whether you're using the
TUI or `--no-tui`.
