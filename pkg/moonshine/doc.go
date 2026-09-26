// Package moonshine provides Go bindings for libmoonshine, the C ABI shared
// library behind https://github.com/moonshine-ai/moonshine.
//
// It provides high-level Go types for Speech-to-Text ([Transcriber], [Stream]),
// Text-to-Speech ([Synthesizer]), Grapheme-to-Phoneme ([Phonemizer]), Text
// Embeddings ([EmbeddingModel]), and model asset management.
//
// # Text Embeddings & Semantic Matching
//
// [EmbeddingModel] loads ONNX-based sentence embedding models (such as
// Gemma-300M, available in quantized "q4" and "q8" variants) to compute
// normalized feature vectors and cosine similarity distances. It directly
// satisfies agentflow.EmbeddingBackend for vector-based semantic phrase matching.
//
// # No cgo
//
// Package moonshine is a pure-Go package (no C compiler or cgo toolchain
// required to build it). It uses github.com/ebitengine/purego to dlopen
// libmoonshine at runtime and call directly into its C ABI functions.
//
// Build libmoonshine from a local moonshine checkout using scripts/build-libmoonshine.sh,
// or fetch prebuilt release binaries using scripts/fetch-libmoonshine.sh, then call
// [Load] before using any STT, TTS, or embedding functions. For production application
// bundling guidance (macOS .app, Windows, Linux, and MCP servers), see
// docs/bundling-libmoonshine.md in the repository root.
package moonshine
