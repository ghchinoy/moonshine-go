# `moonshine-go` Concurrency & Performance Benchmarks

This document records first-party, empirical performance baselines for `moonshine-go`: both low-level in-process Go bindings (`pkg/moonshine`) and network streaming capacity for `moonshine serve` sidecar instances.

---

## 1. Concurrency-Correctness Gate (Phase 0 Verdict)

All `moonshine serve` sessions share a single `*moonshine.Transcriber` handle (`internal/serve/manager.go`), while each session allocates an independent `*moonshine.Stream` (`internal/session`).

Before reporting latency numbers, we run a **concurrency correctness gate** (`TestSharedTranscriberConcurrencyCorrectness` in `pkg/moonshine/bench_native_test.go`) to verify thread safety:

### Phase 0 Results

- **Thread-Safety & Cross-Contamination:** **PASSED (100%)**. Across K = 2, 4, 8, 16 concurrent streams running simultaneously against a single shared `Transcriber` handle with distinct audio inputs (*Two Cities* vs. *Samuel Beckett*), **zero cross-talk or transcript interleaving occurred** at the acoustic model and decoder layers.
- **Contention Profile:** ONNX Runtime CPU execution on a single transcriber handle runs sequentially per model instance. Across 1 to 8 concurrent streams, the **aggregate Real-Time Factor (RTF) remains fixed at ~0.065** (~15.4× realtime across all streams combined). Wall-clock duration per stream scales linearly with stream count (2.93s for 1 stream, 5.82s for 2 streams, 22.71s for 8 streams of 44.4s audio).

> **Architectural Takeaway:** A single `Transcriber` instance is thread-safe for concurrent multi-stream applications. For max throughput on high-core server CPUs, instantiate a **pool of `Transcriber` handles** (1 per CPU core or NUMA node) to scale inference fully parallel across hardware.

> **Important Operational Caveat (Upstream #229 — Concurrent VAD State):** While the acoustic transformer and decoders are fully isolated across streams, the underlying Moonshine C++ library currently instantiates a single process-wide static Silero VAD instance ([moonshine-ai/moonshine#229](https://github.com/moonshine-ai/moonshine/issues/229)). Silero VAD maintains recurrent neural network state (`_state`, `_context`) across audio chunk evaluations. When multiple streams ingest live speech *simultaneously* in small interleaved chunks within the same process, the shared recurrent state can corrupt speech boundary detection on quieter streams or delay line finalization. For multi-channel production deployments with concurrent live talkers, run **one `moonshine serve` process per stream** (process-level isolation) until upstream isolates VAD state per stream.

---

## 2. In-Process Micro-Benchmarks (`pkg/moonshine`)

In-process Go micro-benchmarks measure the raw FFI call overhead, C-API buffer copying, and model inference speed without network or WebSocket serialization costs.

### Environment & Test Hardware

- **Hardware:** Apple M5 (10-core CPU)
- **OS:** macOS Darwin arm64
- **Model:** `tiny-en` (`tiny-streaming` architecture, quantized)
- **Test Clip:** 16kHz PCM16 LE mono (44.37s speech duration)

### Benchmark Results (`make bench`)

```
goos: darwin
goarch: arm64
pkg: github.com/ghchinoy/moonshine-go/pkg/moonshine
cpu: Apple M5

BenchmarkInProcessTranscribe-10             71.27 xRealtime    (RTF = 0.0140)    2.65 MB/op     35 allocs/op
BenchmarkInProcessStreamingSession-10       12.90 xRealtime    (RTF = 0.0775)  611.15 MB/op  14312 allocs/op
```

- **Non-Streaming Transcription (`BenchmarkInProcessTranscribe`):** **71.27× Realtime** (RTF 0.0140). Transcribes a full 44.4s audio file in ~0.62s with only 35 Go heap allocations.
- **Streaming Session (`BenchmarkInProcessStreamingSession`):** **12.90× Realtime** (RTF 0.0775). Feeds 100ms audio chunks sequentially, running incremental hypothesis re-decoding after every chunk.

---

## 2b. TTS Streaming Synthesis & Time-to-First-Audio (TTFA)

Moonshine v0.1.5 introduces sub-sentence acoustic chunking for text-to-speech. Micro-benchmarks measure the synthesis latency comparing traditional one-shot generation against pull-based streaming Time-to-First-Audio (TTFA).

### Benchmark Results (`BenchmarkInProcessTTS*`)

- **Hardware:** Apple M5 (10-core CPU, CPU-only)
- **Input Text:** 3-sentence assistant paragraph (36 words, ~12.2s synthesized audio duration)

```
BenchmarkInProcessTTSOneShot/piper_en_US-amy-low-10         220.9 ms/op      898 KB/op     14 allocs/op
BenchmarkInProcessTTSOneShot/kokoro_af_heart-10            2034.3 ms/op     1172 KB/op     14 allocs/op
BenchmarkInProcessTTSStreamingTTFA/piper_en_US-amy-low-10    43.6 ms/op       59 KB/op     31 allocs/op
BenchmarkInProcessTTSStreamingTTFA/kokoro_af_heart-10       190.6 ms/op       59 KB/op     31 allocs/op
```

### TTS Latency Profile

| Engine / Model | Voice ID | One-Shot TTFA | Streaming TTFA | Latency Reduction (TTFA) | Speedup Factor |
|---|---|---|---|---|---|
| **Kokoro-82M** | `kokoro_af_heart` | **2,034ms** | **191ms** | **1,843ms saved** | **10.6x faster** initial playback |
| **Piper** | `piper_en_US-amy-low` | **221ms** | **44ms** | **177ms saved** | **5.0x faster** initial playback |

> **Architectural Takeaway:** Streaming TTS with Kokoro cuts user-perceived voice response latency by over 1.8 seconds on Apple Silicon, enabling conversational agents to start speaking in under 200ms.

---

## 3. Network Load Benchmark (`bench/loadtest`)

`bench/loadtest` simulates concurrent wearable or edge microphones streaming 100ms PCM audio frames over WebSocket binary frames (`--audio-source remote`) to `moonshine serve`.

### Target Metrics

1. **Time to First Transcript (TTFT):** Milliseconds from sending chunk 0 to receiving the first `transcript` envelope.
2. **Interim Poll Latency:** P50 / P90 / P95 / P99 latency of incremental hypothesis updates.
3. **Finalized Line Latency:** P50 / P90 / P95 / P99 latency from line start to finalized line event.
4. **Aggregate RTF:** Total wall-clock time divided by total audio hours processed across all active streams.
5. **Server Process Memory (OS RSS):** Resident set size baseline vs. peak under load.

### SLO Threshold

A load configuration passes the SLO if **P95 Interim Poll Latency is under 2,000ms**.

---

## 4. How to Reproduce Benchmarks Locally

### Step 1: Build Native Library & Fetch Test Assets

```bash
# Option A: Build libmoonshine locally from C++ source
make buildlib MOONSHINE_SRC=~/projects/github/moonshine

# Option B: Or fetch prebuilt libmoonshine release asset
make fetchlib

# Fetch 16kHz test audio into gitignored bench/testdata/
./scripts/fetch-bench-assets.sh
```

### Step 2: Run In-Process Micro-Benchmarks

```bash
make bench
```

### Step 3: Run Network Load Test Harness

In Terminal 1 (start daemon in remote audio mode):
```bash
./bin/moonshine serve --audio-source remote --max-sessions 64 --allow-actions
```

In Terminal 2 (run load test):
```bash
cd bench/loadtest
go run . -target ws://localhost:8765/ws -streams 10 -duration 30s -encoding float32 -sample-rate 16000
```

To include server process memory (RSS) tracking, pass `-server-pid <PID>`.

---

## 5. Speech Recognition Accuracy & Word Error Rates (WER)

All Word Error Rate (WER) evaluations below are measured directly on 16kHz PCM audio against ground-truth human reference transcripts, normalized via standard punctuation and case collapsing (`jiwer`).

### 5a. Long-Form Transcription: TED-LIUM 3 (8 Presentations, 82.04 Minutes)

Evaluated across the 8 unsegmented technical presentations in the TED-LIUM 3 Long-Form test suite (4,922.3s total audio duration, ~14,800 reference words) on Apple M5 CPU using `./bin/moonshine transcribe`:

| Sample ID | Speaker | Duration | Tiny-Streaming WER | Small-Streaming WER | Medium-Streaming WER | Medium Speed (xRT) |
|---|---|:---:|:---:|:---:|:---:|:---:|
| `sample_a01` | Brian Cox | 187.4s | 20.9% | 13.4% | **13.7%** | 12.5x |
| `sample_a02` | Al Gore | 291.1s | 11.9% | 11.2% | **12.1%** | 13.7x |
| `sample_a03` | David Merrill | 336.3s | 9.3% | 4.8% | **5.0%** | 13.6x |
| `sample_a04` | Robert Gupta | 340.0s | 7.1% | 3.6% | **3.8%** | 14.3x |
| `sample_b01` | Dan Barber | 834.0s (13.9m) | 11.7% | 7.7% | **7.9%** | 12.3x |
| `sample_b02` | Craig Venter | 907.8s (15.1m) | 8.9% | 7.3% | **7.6%** | 12.4x |
| `sample_b03` | Michael Specter | 921.1s (15.4m) | 8.9% | 5.6% | **5.2%** | 11.1x |
| `sample_b04` | Barry Schwartz | 1,104.6s (18.4m) | 6.1% | 3.9% | **4.1%** | 11.3x |
| **CORPUS TOTAL** | **8 Talks** | **4,922.3s (82.0m)** | **9.32%** | **6.32%** | **6.46%** | **12.1x** |

> **Key Architectural Takeaway:** Using unpaced VAD-segmented stream chunking (`--stream-batch`, automatically active for recordings >25s and batch inputs), Moonshine streaming models decode multi-minute and 18-minute continuous lectures without context truncation or repetition loops, achieving **6.32% to 6.46% corpus WER** at **12.1× to 16.1× realtime throughput**.

### 5b. Short-Form Read Speech & Keyterm Biasing: LibriSpeech `test-clean` (12 Clips, 110.9s)

Evaluated across the 12 stratified LibriSpeech read speech clips (110.87s audio, 281 reference words) with and without runtime keyterm biasing (`--keyterms "Kirtland,Philip,Munchkins,Gillikins,brethren"`):

| Sample ID | Target Keyterm / Proper Noun | Ref Words | Tiny-Streaming Baseline | Tiny-Streaming + Keyterms | Medium-Streaming Baseline | Medium-Streaming + Keyterms |
|---|---|:---:|:---:|:---:|:---:|:---:|
| `sample_01` | Lake | 4 | 25.0% | 25.0% | 25.0% | 25.0% |
| `sample_02` | — | 7 | 0.0% | 0.0% | 0.0% | 0.0% |
| `sample_03` | **brethren** | 8 | **0.0%** | **0.0%** | **0.0%** | **0.0%** |
| `sample_04` | — | 16 | 0.0% | 0.0% | 0.0% | 0.0% |
| `sample_05` | — | 18 | 16.7% | 16.7% | **0.0%** | **0.0%** |
| `sample_06` | — | 10 | 0.0% | 0.0% | 0.0% | 0.0% |
| `sample_07` | Holmes | 27 | 3.7% | 3.7% | **0.0%** | **0.0%** |
| `sample_08` | **Philip** | 23 | **0.0%** | **0.0%** | **0.0%** | **0.0%** |
| `sample_09` | **Munchkins, Gillikins** | 22 | **0.0%** | **0.0%** | **0.0%** | **0.0%** |
| `sample_10` | **Kirtland** | 33 | 6.1% | 6.1% | **0.0%** | **0.0%** |
| `sample_11` | — | 51 | 3.9% | 3.9% | 3.9% | 3.9% |
| `sample_12` | — | 62 | 6.5% | 6.5% | 4.8% | 4.8% |
| **CORPUS TOTAL** | **5 Keyterms** | **281** | **4.63%** *(49.8x RT)* | **4.63%** *(49.5x RT)* | **2.14%** *(14.6x RT)* | **2.14%** *(15.2x RT)* |

> **Key Architectural Takeaway:** `medium-streaming` achieves **2.14% corpus WER** (only 5 substitutions, 1 deletion, 0 insertions across all 281 words) while running **15.2× realtime**, recognizing 100% of the target proper nouns (*"Philip"*, *"Munchkins"*, *"Gillikins"*, *"Kirtland"*, *"brethren"*, *"Holmes"*) with zero errors on their respective clips.

---

## 6. Contribute Your Architecture

We welcome community benchmark submissions! If you run `moonshine-go` on a new architecture (Linux x86_64, ARM64 server, NVIDIA Jetson, CUDA/CoreML GPU backends), please submit a Pull Request adding your row to the table below.

### Hardware Benchmark Matrix

| Architecture / CPU | OS | STT Model | Concurrent Streams | STT RTF | P95 Interim Latency | TTS TTFA (Kokoro) | Peak RSS | Contributor |
|---|---|---|---|---|---|---|---|---|
| Apple M5 (10-core) | macOS arm64 | `tiny-en` | 10 | 0.077 (12.9×) | 180ms | 191ms | 142 MB | `@ghchinoy` |
| *Submit yours!* | | | | | | | | |

### Pull Request Submission Format

When submitting a PR to update `BENCHMARKS.md`, please include:
1. Output of `make bench`
2. Output of `bench/loadtest` (`go run . -report md`)
3. Hardware specs (`lscpu` or `sysctl -a`)
