# samples/go-bulk-analysis — batch transcription & corpus analysis

Ingest a directory of audio recordings, transcribe them at **~50–70x real-time**
speed using `moonshine transcribe <dir> --json` (core v0.7.0 native batch mode),
compute throughput and confidence stats, and run a post-processing Gemini LLM pass
to extract themes, cited quotes, and cross-recording synthesis.

## Sample Rating

| Axis | Rating / Details |
|---|---|
| **Tier** | Tier 2 (Go extension via `pkg/serveapi`) |
| **Complexity** | 3/5 |
| **Setup Cost** | Medium (requires moonshine CLI + optional GEMINI_API_KEY) |
| **Pillars** | Observability, Composability, Privacy |
| **Industry / Use Case** | Qualitative UX Research, Call Center QA, Legal Discovery |
| **Appeal** | 5/5 |

---

## What it demonstrates

1. **Faster-than-real-time batch STT** — consumes native `v0.7.0` `moonshine transcribe <dir> --json` batch manifest with worker-pool concurrency (`-concurrency 8`) and single-instance ONNX model reuse.
2. **Citation primitives & confidence tracking** — preserves per-line timestamps
   (`Line.StartTime`), per-word timings (`Word.Start`/`End`), speaker labels
   (`Line.SpeakerSpans`), and mean confidence scores (`Line.MeanConfidence`).
3. **Structured dual output**:
   - **`corpus_report.md`** — a primary human-readable Markdown report with a
     throughput summary table, LLM executive synthesis, thematic clusters,
     and verbatim quotes cited back to `[filename @ MM:SS (confidence %)]`.
   - **`corpus_manifest.json`** — a secondary agent-friendly JSON manifest with
     full per-file lines, word timings, speaker spans, and aggregate
     metrics (`TotalAudioSec`, `AggregateRTF`, `ConfidenceHistogram`).
4. **Offline / No-LLM mode** — pass `-no-llm` to run pure batch transcription
   and stats generation locally with zero network calls or API keys.

---

## Quickstart

### 1. Build the moonshine CLI

If you haven't built the moonshine CLI binary yet:

```sh
cd ../.. # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib" # see repo README if libmoonshine is not fetched yet
make build
```

### 2. Run the sample

Transcribe a folder of `.wav` files (e.g., using test assets or your own recordings):

```sh
cd samples/go-bulk-analysis

# Set your Gemini API key for the post-transcription analysis pass
export GEMINI_API_KEY="your-api-key"

# Run batch transcription + analysis over a folder of WAV files
go run . -dir /path/to/wav/files
```

### Options

```sh
# Skip the LLM pass (transcribe & compute stats only, offline):
go run . -dir /path/to/wav/files -no-llm

# Enable speaker diarization:
go run . -dir /path/to/wav/files -identify-speakers

# Set custom concurrency and output paths:
go run . -dir /path/to/wav/files -concurrency 8 -out-md my_report.md -out-json my_manifest.json
```

---

## Output Example (`corpus_report.md`)

```markdown
# Bulk Audio Analysis Report

## Corpus Throughput & Performance

| Metric | Value |
|---|---|
| **Total Recordings** | 5 files (5 successful, 0 failed) |
| **Total Audio Duration** | 240.50 seconds (4.0 minutes) |
| **Wall-Clock Time** | 3.65 seconds |
| **Aggregate Speed (RTF)** | **65.9x real-time** |
| **Mean STT Confidence** | **94.2%** |
| **Confidence Distribution** | >=90%: 4, 80-89%: 1, <80%: 0 |

---

## Executive Synthesis
...
```
