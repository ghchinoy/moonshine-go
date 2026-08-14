// Package agentflow implements a Go-native voice agent DSL for building speech
// interfaces, mirroring upstream Moonshine Voice's AgentFlow API across Swift,
// Python, and Java (see interactive explainer at https://moonshine.ai/agent-flow/).
//
// AgentFlow enables straight-line, multi-turn conversational dialogs (Say, Ask,
// Confirm, Choose) driven by streaming speech-to-text input, fuzzy trigger-phrase
// matching, and text-to-speech feedback.
//
// Like pkg/serveapi, pkg/agentflow is a stdlib-only Go package that builds
// cleanly under CGO_ENABLED=0 without requiring C toolchains or libmoonshine
// at compile time.
package agentflow
