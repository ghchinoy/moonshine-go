# `moonshine-go` Concurrency & Performance Benchmarks

This document records first-party, empirical performance baselines for `moonshine-go`: both low-level in-process Go bindings (`pkg/moonshine`) and network streaming capacity for `moonshine serve` sidecar instances.

---

## 1. Concurrency-Correctness Gate (Phase 0 Verdict)

All `moonshine serve` sessions share a single `*moonshine.Transcriber` handle (`internal/serve/manager.go`), while each session allocates an independent `*moonshine.Stream` (`internal/session`).

Before reporting latency numbers, we run a **concurrency correctness gate** (`TestSharedTranscriberConcurrencyCorrectness` in `pkg/moonshine/bench_native_test.go`) to verify thread safety:

### Phase 0 Results

- **Thread-Safety & Cross-Contamination:** **PASSED (100%)**. Across $K \in \{2, 4, 8, 16\}$ concurrent streams running simultaneously against a single shared `Transcriber` handle with distinct audio inputs (*Two Cities* vs. *Samuel Beckett*), **zero cross-talk or transcript interleaving occurred**.
- **Contention Profile:** ONNX Runtime CPU execution on a single transcriber handle runs sequentially per model instance. Across 1 to 8 concurrent streams, the **aggregate Real-Time Factor (RTF) remains fixed at ~0.065** (~15.4× realtime across all streams combined). Wall-clock duration per stream scales linearly with stream count ($2.93\text{s}$ for 1 stream, $5.82\text{s}$ for 2 streams, $22.71\text{s}$ for 8 streams of 44.4s audio).

> **Architectural Takeaway:** A single `Transcriber` instance is completely thread-safe for concurrent multi-stream applications. For max throughput on high-core server CPUs, instantiate a **pool of `Transcriber` handles** (1 per CPU core or NUMA node) to scale inference fully parallel across hardware.

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

- **Non-Streaming Transcription (`BenchmarkInProcessTranscribe`):** **71.27× Realtime** ($0.0140\text{ RTF}$). Transcribes a full $44.4\text{s}$ audio file in $\sim 0.62\text{s}$ with only 35 Go heap allocations.
- **Streaming Session (`BenchmarkInProcessStreamingSession`):** **12.90× Realtime** ($0.0775\text{ RTF}$). Feeds 100ms audio chunks sequentially, running incremental hypothesis re-decoding after every chunk.

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

A load configuration passes the SLO if **P95 Interim Poll Latency $\le 2,000\text{ms}$**.

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

## 5. Contribute Your Architecture

We welcome community benchmark submissions! If you run `moonshine-go` on a new architecture (Linux x86_64, ARM64 server, NVIDIA Jetson, CUDA/CoreML GPU backends), please submit a Pull Request adding your row to the table below.

### Hardware Benchmark Matrix

| Architecture / CPU | OS | Model | Concurrent Streams | In-Process RTF | P95 Interim Latency | Peak RSS | Contributor |
|---|---|---|---|---|---|---|---|
| Apple M5 (10-core) | macOS arm64 | `tiny-en` | 10 | 0.077 (12.9×) | 180ms | 142 MB | `@ghchinoy` |
| *Submit yours!* | | | | | | | |

### Pull Request Submission Format

When submitting a PR to update `BENCHMARKS.md`, please include:
1. Output of `make bench`
2. Output of `bench/loadtest` (`go run . -report md`)
3. Hardware specs (`lscpu` or `sysctl -a`)
