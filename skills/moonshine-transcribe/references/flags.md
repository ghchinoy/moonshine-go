# `moonshine transcribe` reference

Full detail for the `moonshine-transcribe` skill. Load this only when the
top-level `SKILL.md` workflow isn't enough (e.g. debugging an error, or a
flag not covered there).

## Usage

```
moonshine doctor [flags]
moonshine transcribe <file|gs://bucket/object> [flags]
```

## `moonshine doctor`

Run this first if anything's unclear -- it checks build tools, whether
`libmoonshine` resolves/loads, and whether an STT model is downloaded, and
prints exact fix commands. `--language`/`--arch` select which model pair to
check for (same defaults as `transcribe`: `en`/`tiny`). Exits non-zero if
any check `[FAIL]`s; `--json` gives `{"checks": [{"name", "status",
"detail"}, ...]}` with `status` one of `ok`/`warn`/`fail`/`skip`.

## `transcribe` flags

| Flag              | Default             | Config key       | Notes |
|-------------------|---------------------|-------------------|-------|
| `--language`      | `en`                | `stt.language`    | Must match the language passed to `moonshine setup`. |
| `--arch`           | `tiny`              | `stt.arch`        | One of: `tiny`, `base`, `tiny-streaming`, `base-streaming`, `small-streaming`, `medium-streaming`. Must match a model already downloaded via `moonshine setup --arch <arch>`. |
| `--providers`      | `""` (CPU only)     | (none)            | Comma-separated ONNX Runtime execution providers, e.g. `CoreML,CPU` on macOS. Leave unset -- CPU is the only provider verified fast/correct for every arch (see `docs/hardware-acceleration.md`); CoreML has been measured slower or outright broken for some archs on the vendored onnxruntime build. |
| `--with-audio`     | `false`             | (none)            | Include each line's raw per-line audio samples in `--json` output. Large; leave off unless the caller specifically needs raw samples. |
| `--identify-speakers` | `false`          | (none)            | Enable speaker diarization: adds a `speaker_spans` array per line in `--json`, and prefixes text output with a speaker label like `[S0]`. Adds significant compute; implies `--word-timestamps`. |
| `--word-timestamps` | `false`            | (none)            | Enable per-word timing: adds a `words` array per line in `--json` (`{text, start, end, confidence}`). |
| `-o, --output`     | `""`                | (none)            | Also write the transcript to this file, in addition to stdout. Plain text `[  0.00s] text` lines, or the same JSON shape as `--json` stdout if `--json` is also set. |
| `--json` (global)  | `false`             | `output.json`     | Root-level persistent flag (also works on other subcommands). Always use this when parsing output programmatically. |
| `--lib-dir` (global) | `""` (see below)  | `lib.dir`         | Directory containing `libmoonshine.{dylib,so}`. Default: `$MOONSHINE_LIB_DIR`, else `./.moonshine/lib`. |
| `--model-dir` (global) | platform cache dir | `model.dir`   | Root directory for downloaded model assets. Default: `~/Library/Caches/moonshine_voice` (macOS), `$XDG_CACHE_HOME/moonshine_voice` or `~/.cache/moonshine_voice` (Linux). Overridable via `$MOONSHINE_MODEL_DIR` or `$MOONSHINE_VOICE_CACHE`. |

`moonshine live` (interactive mic transcription, out of scope for this
skill -- see SKILL.md's Non-goals) has the same `--identify-speakers`/
`--word-timestamps` flags, plus `--diarization-cluster-cadence`/
`--diarization-analyze-cadence`/`--diarization-cluster-window-sec` tuning
knobs for streaming re-clustering cost.

Precedence for every flag with a config key (highest wins): CLI flag > env
var > `config.yaml` > built-in default. Run `moonshine config list` to see
the currently effective value and its source.

## `--json` output shape

```json
{
  "lines": [
    {
      "text": "It was the best of times, it was the worst of times.",
      "audio_data": null,
      "start_time": 0.99,
      "duration": 3.97,
      "id": 3349676881725816990,
      "is_complete": true,
      "words": null,
      "speaker_spans": null
    }
  ],
  "stats": {
    "model_load_ms": 80.1,
    "download_ms": 0,
    "decode_ms": 235.4,
    "inference_ms": 619.2,
    "audio_duration_sec": 44.37,
    "real_time_factor": 71.7
  }
}
```

- `duration` is the line's length in seconds -- **not** an absolute end
  time; add it to `start_time` if you need one.
- `lines[].audio_data` is only populated when `--with-audio` is passed;
  otherwise it's omitted/null.
- `lines[].words` (`[{text, start, end, confidence}, ...]`) is only
  populated when `--word-timestamps` (or `--identify-speakers`, which
  implies it) is passed.
- `lines[].speaker_spans` (`[{start_time, duration, speaker_id,
  speaker_index, start_char, end_char}, ...]`) is only populated when
  `--identify-speakers` is passed. `speaker_index` counts speakers in order
  of first appearance (0, 1, 2, ...) and is what the text-output `[S0]`
  label is derived from; `speaker_id` is a larger, stable-but-arbitrary
  identifier, not meant for display.
- `stats.download_ms` is only present (nonzero) when the input was a
  `gs://` URI.
- `stats.real_time_factor` is `audio_duration_sec / (inference_ms/1000)` --
  higher is faster than real time (e.g. `71.7x` means 44s of audio
  transcribed in ~0.6s of inference).

## Error modes and fixes

`moonshine doctor` catches the first two rows below (and the STT-model one)
proactively -- prefer running it over waiting for these errors.

| Error (substring)                                   | Cause                                   | Fix |
|------------------------------------------------------|------------------------------------------|-----|
| `moonshine: could not find libmoonshine`             | Native lib not built / not found         | `make buildlib MOONSHINE_SRC=/path/to/moonshine`, then set `MOONSHINE_LIB_DIR` or `--lib-dir` to `.moonshine/lib` (or wherever it was built to). See README.md "Build libmoonshine". |
| `moonshine: dlopen ...`                               | Lib found but failed to load (ABI mismatch, missing onnxruntime next to it, etc.) | Rebuild via `scripts/build-libmoonshine.sh`; on macOS ensure onnxruntime dylib sits next to `libmoonshine.dylib` (the build script handles this automatically). |
| `hint: run \`moonshine setup --language ... --arch ...\` first` | Model files for that (language, arch) pair aren't downloaded | Run the suggested `moonshine setup` command exactly as printed. |
| `audio: unsupported file extension ...`               | Input isn't `.wav`                        | Convert first: `ffmpeg -i in.mp3 -ar 16000 -ac 1 out.wav`. |
| `audio: ... is not a valid WAV file` / `invalid sample rate` | Corrupt or malformed WAV                | Re-export/convert the source audio; verify with `ffprobe`. |
| GCS download errors (e.g. `403`, `googleapi: Error`) | Application Default Credentials not set/valid for the `gs://` bucket | Ensure `gcloud auth application-default login` has been run, or `GOOGLE_APPLICATION_CREDENTIALS` points at a valid service account key with read access to the bucket. |

## Related config keys (not flags, but affect behavior)

| Key                  | Env var(s)                                  | Purpose |
|-----------------------|----------------------------------------------|---------|
| `moonshine.src_dir`   | `MOONSHINE_SRC`                              | Local moonshine checkout path; only relevant for `make buildlib` / TTS, not transcription itself. |

See `moonshine config list` for the authoritative, live set of keys and
their current values/provenance -- this table may drift from the CLI over
time; treat the CLI's own `--help` and `config list` output as the source
of truth if anything here looks inconsistent.
