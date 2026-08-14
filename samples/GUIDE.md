# Developer Guide: Moonshine-Go Sample Architecture, Rubric, and Industry Matrix

This guide brings together the conceptual architecture, latency analysis, sample ratings, and vertical industry scenarios behind `moonshine serve` and its sample catalog.

While individual sample `README.md` files focus on mechanical run instructions and code structure, this guide explains **why** the samples are structured in Tiers, **how** the local cascade outperforms cloud speech-to-speech models on control and latency, and **where** each pattern applies in real-world software engineering.

---

## 1. The Cascade Thesis & Latency Waterfall

For several years, speech-to-speech (S2S) model architectures argued that the classic voice cascade (STT → LLM → TTS) was too slow due to serialization overhead. However, the critique was strictly about milliseconds, not capability.

`moonshine serve` restores the viability of the local cascade by removing inter-stage IPC taxes through purego bindings and zero-cgo transcription hot-paths.

### Latency Comparison: Cloud Speech-to-Speech vs. Moonshine Local Cascade

| Stage | Cloud Speech-to-Speech (S2S) | Moonshine Local Cascade (`moonshine serve`) |
|---|---|---|
| **Audio Ingress / Network Transport** | 150ms – 350ms (cloud WebSocket upload) | **0ms – 5ms** (local IPC / loopback WS) |
| **STT Time-to-First-Token (TTFT)** | 300ms – 600ms | **25ms – 75ms** (`tiny-streaming` ONNX) |
| **STT Per-Line Finalization** | 400ms – 800ms | **30ms – 90ms** (`LastLatencyMs`) |
| **Fast-Path Intent Execution** | *Not available* (requires full LLM turn) | **< 1ms** (`AgentFlow` / `IntentMatcher` rule) |
| **RAG / Tool Call Round-Trip** | 400ms – 1000ms | **10ms – 50ms** (`StaticRetriever` / local RPC) |
| **TTS First Audio Chunk** | 200ms – 500ms | **15ms – 40ms** (`audio.PlayFloat32` / local Piper) |
| **Total End-to-End Latency** | **1,050ms – 2,500ms** | **40ms – 120ms** (fast-path) / **180ms – 450ms** (full LLM) |

By eliminating cloud network round-trips and using fast-path intent matching (`pkg/agentflow` / `IntentMatcher`) before invoking LLMs, the local cascade achieves sub-100ms response times for deterministic control actions.

---

## 2. Sample Catalog Rating Matrix

Every sample in `samples/` self-reports its rating schema in its own `README.md` (see `CONTRIBUTING.md`). Below is the aggregated matrix across all available samples:

| Sample | Tier | Complexity | Setup Cost | Demonstrated Pillars | Primary Vertical / Use Case | Appeal |
|---|---|:---:|:---:|---|---|:---:|
| **[go-listen](go-listen/)** | Tier 0 | 1/5 | Low | Composability | Real-time transcript feed (Go) | 2/5 |
| **[python-listen](python-listen/)** | Tier 0 | 1/5 | Low | Composability | Real-time transcript feed (Python) | 2/5 |
| **[grpc-listen](grpc-listen/)** | Tier 0 | 1/5 | Low | Composability, Observability | High-throughput gRPC transcript pipeline | 3/5 |
| **[browser-listen](browser-listen/)** | Tier 1 | 2/5 | Medium | Composability, Privacy | Zero-install browser microphone capture | 4/5 |
| **[browser-cascade-faq](browser-cascade-faq/)** | Tier 1 | 3/5 | Medium | Control, Privacy, Composability | Zero-install browser voice FAQ with Web Audio TTS | 5/5 |
| **[go-cascade-faq](go-cascade-faq/)** | Tier 1 | 3/5 | Medium | Control, Observability, Privacy, Composability | Flagship Go offline RAG voice agent | 4/5 |
| **[go-domain-customization](go-domain-customization/)** | Tier 1 / Tier 2 | 3/5 | Low | Control, Composability, Observability, Privacy | Dynamic keyterm biasing & passage context extraction | 5/5 |
| **[python-agent](python-agent/)** | Tier 2 | 4/5 | Medium | Control, Composability | Multi-turn spoken LLM agent with tools | 4/5 |
| **[go-bulk-analysis](go-bulk-analysis/)** | Tier 2 | 3/5 | Medium | Observability, Composability, Privacy | Batch audio corpus transcription & LLM report synthesis | 5/5 |
| **[go-embedded](go-embedded/)** | Native / in-process | 2/5 | Medium | Privacy, Composability | Direct in-process batch & streaming STT (no daemon) | 4/5 |
| **[mcp-transcribe](mcp-transcribe/)** | Native / in-process | 3/5 | Medium | Composability, Privacy | Embedded MCP server exposing in-process STT tool | 5/5 |
| **[desktop-app](desktop-app/)** | Native / in-process | 3/5 | High | Privacy, Composability | Native Wails v2 GUI app with in-process STT (no daemon) | 5/5 |

---

## 3. Industry & Vertical Applications Matrix

| Industry / Vertical | Primary Sample Pattern | Concrete Real-World Workflow | Control & Compliance Angle |
|---|---|---|---|
| **Healthcare & Clinical** | `browser-cascade-faq` / `go-cascade-faq` | Ambient clinical dictation, hands-free sterile field commands, medication dosage verification | PHI never leaves local premises in air-gapped mode. Dosage read-backs use deterministic regex fast-paths to eliminate LLM hallucinations. |
| **Legal & Compliance** | `grpc-listen` / `go-bulk-analysis` | Deposition recording, compliance logging, cross-case transcript search with citations | Transcript is an immutable, timestamped record. Verbatim quotes carry `[filename @ MM:SS (confidence %)]` citations for auditability. |
| **Financial Services & Contact Centers** | `grpc-listen` / `go-cascade-faq` | Live agent-assist whisper prompts, trade execution voice confirmation, disclosure logging | Regulatory recordkeeping requires timestamped line events. Money-moving actions require deterministic confirmation, not probabilistic tool calls. |
| **Industrial IoT & Field Logistics** | `go-cascade-faq` | Hands-free equipment inspection, voice work-orders, safety checklist read-back | Air-gapped deployment functions reliably on remote oil rigs or factory floors without internet connectivity. |
| **E-Commerce & Kiosks** | `browser-cascade-faq` | Zero-install customer support voice kiosk in browser or tablet | Runs directly in web browsers with Web Audio TTS playback and zero client-side installation. |
| **Developer Tooling & Sysadmin** | `go-bulk-analysis` / `python-agent` | Voice-driven terminal/tmux control, automated meeting archive indexing, system command execution | Fast-path intent interception allows instant execution ("interrupt", "run command") with logged tool arguments and safety gating. |

---

## 4. Integration Tier Selection Guide

When deciding how to integrate `moonshine serve` into your application:

- **Choose Tier 0** if you only need to display, log, or store live transcripts without speaking back or taking control actions.
- **Choose Tier 1** if you are building an external agent in non-Go languages (Python, Node.js, Rust) that sends `speak` or `session.*` action JSON back over WebSocket.
- **Choose Tier 2** if you are building a Go application using `pkg/serveapi` (`AgentRunner`, `Retriever`) and `pkg/agentflow` (`AgentFlow`, `PhraseMatcher`, `Dialog`) for type-safe, sub-millisecond fast-path intent routing and multi-turn voice flows.

---

## 5. Pattern Selection Guide: Fast-Path IntentMatcher vs. AgentFlow DSL vs. LLM Agent

When building an agent on top of `moonshine serve`, developers choose from three primary control & decision patterns depending on latency, complexity, and non-determinism requirements:

| Pattern | How it works | Best used for | Reference implementation |
|---|---|---|---|
| **Fast-Path Regex (`IntentMatcher`)** | Zero-dependency regex rules compiled ahead of time; returns synchronous `ActionRequest`s (`session.pause`, `session.resume`, `session.stop`). | Sub-millisecond, 100% deterministic single-shot control actions with no model downloads or prompt turns. | `internal/serve/intent.go` |
| **Voice Agent DSL (`pkg/agentflow`)** | Go-native voice agent framework (`AgentFlow`, `PhraseMatcher`, `Dialog`) supporting `Say`, `Ask`, `Confirm`, and `Choose` flows with fuzzy embedding matching. | Structured, multi-turn voice dialogs, prompt retries, confirmation flows, and interactive voice surveys. | `pkg/agentflow`, `samples/go-cascade-faq` (external IPC) & `moonshine serve --agent agentflow` (in-process) |
| **Spoken LLM Agent (`GeminiAgent`)** | Streaming transcript line passed to a large language model (e.g. Gemini 2.5 Flash / Lite) with function calling / MCP tools. | Open-ended reasoning, unstructured Q&A, multi-step tool calls, and complex dialogs requiring general intelligence. | `internal/serve/gemini.go` & `samples/python-agent` |

### Selection Decision Matrix

- Use **`IntentMatcher` (Regex)** if you need guaranteed <1ms interception of fixed system verbs (`"stop listening"`, `"interrupt"`, `"clear"`) without any ML model overhead or memory allocation.
- Use **`pkg/agentflow` (AgentFlow DSL)** if you need structured multi-turn conversation flows (`d.Ask`, `d.Confirm`, `d.Choose`), phrase-group trigger matching, or flow-scoped cancellation and restart logic.
- Use **LLM Function Calling** if the user's request is ambiguous, unconstrained, or requires reasoning over external databases or MCP tools before taking action.
- Use **`CompositeHandler`** to chain them: run a fast-path regex or AgentFlow trigger first, and fall through to an LLM agent on a miss.
