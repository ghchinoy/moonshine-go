# Hardware acceleration (`--providers`)

`transcribe` and `live` both accept `--providers`, passed straight through to
libmoonshine's `ort_providers` option (ONNX Runtime execution providers,
comma-separated, e.g. `CoreML,CPU`). **The default is CPU-only on every
platform**, which is a deliberate choice based on hands-on measurement, not
an oversight -- read on before turning CoreML on.

## Why CPU is the default on macOS

moonshine's own header (`moonshine-c-api.h`) documents `CoreML,CPU` as a
supported combination on macOS. In practice, against the onnxruntime build
this project vendors (`core/third-party/onnxruntime`, currently 1.23.2), and
these specific model architectures, enabling it was a net loss:

| Arch | Providers | Inference time (44.4s audio) | RTF | Result |
|---|---|---|---|---|
| `tiny` | CPU only | 655-684ms | ~65-68x | correct |
| `tiny` | `CoreML,CPU` (cold) | 1227ms | 36x | correct, but 2x slower |
| `tiny` | `CoreML,CPU` (warm cache) | 1174ms | 38x | correct, still slower |
| `base` | CPU only | 1010-1059ms | ~42-44x | correct |
| `base` | `CoreML,CPU` | -- | -- | **hard error**: `ORT Error: Non-zero status code ... Unable to compute the prediction` |
| `tiny-streaming` | CPU only | 887-936ms | ~47-50x | correct |
| `tiny-streaming` | `CoreML,CPU` | 1440ms | 31x | **wrong**: transcript lines come back empty |

(Measured on Apple Silicon, single 44.4s test clip, non-streaming
`transcribe`; "cold" vs "warm" refers to CoreML's model-compilation cache --
see below.)

In short: for `tiny`, CoreML is consistently *slower* than plain CPU. For
`base` it errors outright. For `tiny-streaming` it silently produces empty
output -- the worst possible failure mode, since nothing in the exit code or
stderr necessarily makes that obvious if you're not comparing against a
CPU-only run.

This isn't necessarily a permanent state of affairs -- it reflects one
onnxruntime version, these specific ONNX graphs, and Apple Silicon's CPU
already being very fast (well-optimized NEON/AMX matmul) for models this
small. CoreML/ANE execution tends to pay off more for larger models where
per-inference dispatch/partitioning overhead is a smaller fraction of total
compute. If you want to experiment:

```sh
moonshine transcribe --providers CoreML,CPU some.wav
```

...and compare against the default before trusting the output, especially
for any arch other than plain `tiny`. If you hit the `base` error or
suspiciously short/empty transcripts, that's this issue, not a
misconfiguration -- fall back to `--providers ""` (or just omit the flag).

## CoreML compilation cache

When CoreML is requested, `--providers` also sets libmoonshine's
`coreml_cache_dir` option to `<model.dir>/ort-coreml-cache`. CoreML compiles
each model graph on first load; without a persistent cache directory, that
compilation cost (several hundred ms, see "cold" vs "warm" above) is paid on
every single CLI invocation. This is handled automatically -- you don't need
to set it yourself.

## Other providers

The C API also documents `NNAPI,CPU` for Android, which isn't relevant to
this CLI. Nothing else (CUDA, DirectML, etc.) is currently wired up in the
vendored onnxruntime build; `--providers` will pass through whatever string
you give it, but unsupported/unavailable providers will fail at transcriber
load time the same way `CoreML,base` did above.

## If you re-measure this

If you're on different hardware, a newer onnxruntime, or a future moonshine
release and get different results, that's useful to know -- these numbers
are a snapshot from one measurement session, not a guarantee. The
methodology was simple and reproducible:

```sh
moonshine --json transcribe --arch <arch> --providers "<providers>" <file>.wav 2>/dev/null \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['stats'])"
```

Run it once per (arch, providers) combination on the same input file and
compare `inference_ms` / `real_time_factor`.
