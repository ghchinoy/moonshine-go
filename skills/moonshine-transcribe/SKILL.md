---
name: moonshine-transcribe
description: Transcribe local audio files or gs:// GCS objects to text using the moonshine-go CLI's "transcribe" command (Moonshine STT via libmoonshine). Use when the user asks to transcribe, caption, or get a text transcript of a .wav file (or other audio convertible to .wav), in this repo or any project that vendors this CLI.
compatibility: Requires a built ./bin/moonshine binary (or one on PATH), a built libmoonshine.{dylib,so} (MOONSHINE_LIB_DIR), and a downloaded STT model (moonshine setup). gs:// input additionally requires valid Google Cloud Application Default Credentials.
---

# Transcribing audio with moonshine

This skill drives the existing `moonshine transcribe` CLI command instead of
reimplementing STT. Never call into `internal/moonshine` or libmoonshine
directly from a script -- always shell out to the `moonshine` binary.

## 1. Check prerequisites before transcribing

Run `moonshine doctor` (or `moonshine --json doctor` for parseable output)
first -- it checks build tools, whether `libmoonshine` resolves/loads, and
whether an STT model is downloaded for `--language`/`--arch`, all in one
shot, with the exact fix command for anything missing:

```sh
moonshine doctor
moonshine doctor --language en --arch tiny   # check a specific model pair
```

If it reports `[FAIL]` on `libmoonshine`, that's a one-time build step
(`make buildlib MOONSHINE_SRC=/path/to/moonshine`, see README.md's "Build
libmoonshine" section) -- don't try to work around it, tell the user to run
that first, or run it yourself if a moonshine checkout path is known. If it
reports `[WARN]` on the STT model, run the `moonshine setup ...` command it
prints (this only needs to happen once per `(language, arch)` pair).

If `doctor` reports no failures, skip straight to step 2. (`moonshine
config list --json` also shows the effective `lib.dir`/`model.dir` and
where each value came from, if you need finer-grained config detail than
`doctor` gives you.)

## 2. Run the transcription

```sh
moonshine transcribe --json path/to/audio.wav
```

For a GCS object, pass the `gs://` URI directly -- the CLI downloads it
first via Application Default Credentials:

```sh
moonshine transcribe --json gs://my-bucket/audio.wav
```

Non-`.wav` input (mp3, m4a, etc.) is **not** supported directly; convert
first:

```sh
ffmpeg -i input.mp3 -ar 16000 -ac 1 audio.wav
moonshine transcribe --json audio.wav
```

Always pass `--json` when consuming output programmatically -- it's the
only stable, parseable format. Without it, timing/progress output goes to
stderr and only human-formatted `[  0.99s] text` lines go to stdout.

### Optional: speaker diarization and word timestamps

Both are opt-in and off by default (diarization adds significant compute):

```sh
moonshine transcribe --json --identify-speakers path/to/audio.wav   # adds speaker_spans per line
moonshine transcribe --json --word-timestamps path/to/audio.wav     # adds words per line
```

`--identify-speakers` automatically enables word timestamps too. Only turn
these on if the user actually asked for speaker labels or word-level
timing -- they're not needed for a plain transcript and diarization in
particular slows things down noticeably.

## 3. Parse the `--json` output

`stdout` is a single JSON object:

```json
{
  "lines": [
    {
      "text": "It was the best of times...",
      "start_time": 0.99,
      "duration": 3.2,
      "id": 3349676881725816990,
      "is_complete": true
    }
  ],
  "stats": {
    "model_load_ms": 80.1,
    "decode_ms": 235.4,
    "inference_ms": 619.2,
    "audio_duration_sec": 44.37,
    "real_time_factor": 71.7
  }
}
```

`duration` is how long the line is, in seconds (not an absolute end time --
add it to `start_time` if you need that). `download_ms` in `stats` is only
present for `gs://` input. `words` (per-word `{text, start, end,
confidence}`) and `speaker_spans` (per-speaker `{start_time, duration,
speaker_id, speaker_index, start_char, end_char}`) are only present when
`--word-timestamps`/`--identify-speakers` were passed. See
`references/flags.md` for the exact field types and every CLI flag/config
key/error case.

If the user wants a plain transcript file rather than parsed JSON, use
`-o`/`--output` instead of (or in addition to) capturing stdout:

```sh
moonshine transcribe -o transcript.txt path/to/audio.wav       # plain text
moonshine transcribe --json -o transcript.json path/to/audio.wav  # JSON to file too
```

## 4. Report results

Summarize the transcript for the user (or write it where they asked), and
mention `stats.real_time_factor` / `audio_duration_sec` only if they seem
interested in performance, not by default.

## Non-goals

- **Live/streaming transcription** (`moonshine live`) is an interactive mic
  session with a TUI -- not something to script from this skill. If the
  user wants live mic transcription, tell them to run `moonshine live`
  themselves in a terminal.
- **Text-to-speech** (`moonshine tts`) is a separate concern; this skill is
  transcription (STT) only.

See `references/flags.md` for the full flag/config/error reference.
