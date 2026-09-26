// Package agentflow implements a Go-native voice agent DSL for building speech
// interfaces, mirroring upstream Moonshine Voice's AgentFlow API across Swift,
// Python, and Java (see interactive explainer at https://moonshine.ai/agent-flow/).
//
// AgentFlow enables straight-line, multi-turn conversational dialogs (Say, Ask,
// Confirm, Choose) driven by streaming speech-to-text input, fuzzy trigger-phrase
// matching, and text-to-speech feedback.
//
// # Semantic Matching with Embeddings
//
// By default, PhraseMatcher performs normalized string and substring matching.
// Providing an [EmbeddingBackend] via [AgentFlow.SetEmbeddingBackend] enables
// semantic vector matching: candidate phrases are embedded once into normalized
// float32 vectors, and user speech is matched against phrases using cosine similarity
// against the configured [AgentFlow.TriggerThreshold].
//
// The [EmbeddingBackend] interface is satisfied directly by [pkg/github.com/ghchinoy/moonshine-go/pkg/moonshine.EmbeddingModel].
//
// Like pkg/serveapi, pkg/agentflow is a stdlib-only Go package that builds
// cleanly under CGO_ENABLED=0 without requiring C toolchains or libmoonshine
// at compile time.
package agentflow
