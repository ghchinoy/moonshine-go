// Package serveapi is the public, Go-native extension surface for the
// "moonshine serve" agentic voice sidecar.
//
// It exists so that Go programs outside this module can implement the
// sidecar's extension points -- an [AgentHandler] that reacts to finalized
// utterances, a [Retriever] for lookup/RAG, an [LLMClient] behind the
// built-in agent, or an [AudioSource] that feeds audio from somewhere other
// than the local microphone -- and link them in-process (the "Tier 2"
// integration path in docs/quickstart-voice-agent.md).
//
// The primary, language-agnostic extension surface is still IPC/JSON over the
// WebSocket and gRPC transports (Tier 0 / Tier 1). This package is an
// additional on-ramp for Go, not a replacement for that surface.
//
// # No cgo
//
// serveapi is a leaf package: it imports only the standard library. It never
// imports internal/session or internal/audio (which require cgo via the mic
// backend), so it -- and anything that depends only on it -- builds with
// CGO_ENABLED=0. The wire/data types here are deliberate shadow structs, not
// aliases of the internal moonshine types, so the public contract is
// insulated from internal churn. The conversion between internal types and
// these public types lives in internal/serve, which already depends on the
// cgo-bound packages.
//
// # Fast/Slow Confidence Routing
//
// Each [Line] emitted in [TranscriptEvent] carries per-word confidence scores
// ([Word.Confidence]) and aggregate line confidence helpers ([Line.MeanConfidence]
// and [Line.MinConfidence], values 0.0 to 1.0). Agents can use confidence scores
// to route finalized lines down different execution paths:
//
//   - High confidence (e.g. MeanConfidence >= 0.80): dispatch to fast-path,
//     deterministic handlers like [CompositeHandler] or intent matchers.
//   - Low confidence (e.g. MeanConfidence < 0.80): ask the user to clarify or repeat,
//     or escalate to an LLM with extra grounding context.
package serveapi
