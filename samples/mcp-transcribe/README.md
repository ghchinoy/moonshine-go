# samples/mcp-transcribe — MCP Speech-to-Text tool server via `pkg/moonshine`

In-process, zero-cgo Speech-to-Text tool server for the [Model Context Protocol (MCP)](https://modelcontextprotocol.io), built with [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) and [`pkg/moonshine`](../../pkg/moonshine).

This sample demonstrates embedding `pkg/moonshine` directly inside a standalone MCP server process without requiring a `moonshine serve` sidecar daemon or C toolchain (`CGO_ENABLED=0`). Higher-level agent hosts (Claude Desktop, Cursor, IDE MCP integrations, or custom agent runners) can invoke the `transcribe` tool to transcribe local `.wav` audio files directly.

## Sample Rating

| Axis | Rating / Details |
|---|---|
| **Tier** | Native / in-process (no daemon) |
| **Complexity** | 3/5 |
| **Setup Cost** | Medium (requires local `libmoonshine.{dylib,so}` + downloaded STT model) |
| **Pillars** | Composability, Privacy |
| **Industry / Use Case** | Developer Tooling, AI Agent Extensions, MCP Tool Integration |
| **Appeal** | 5/5 |

---

## Privacy & Local Processing Guarantee

> **Privacy Note:** Audio is transcribed 100% locally in-process via `pkg/moonshine`'s ONNX Runtime inference engine. No raw audio data is ever uploaded, streamed, or transmitted to external cloud servers — even if the input audio file resides on remote storage.

---

## What it demonstrates

1. **Native MCP Server** — runs an official Go SDK (`github.com/modelcontextprotocol/go-sdk`) MCP server exposing the `transcribe` tool over `stdio` or Streamable HTTP.
2. **Direct `pkg/moonshine` Embedding** — loads `libmoonshine` dynamically at runtime via `ebitengine/purego` (`moonshine.Load()`) with zero cgo build dependencies (`CGO_ENABLED=0`).
3. **Transcriber Caching** — lazily loads STT models on the first tool call and reuses the cached `*moonshine.Transcriber` handle across subsequent tool calls for optimal performance.
4. **Hosted HTTP Security** — includes optional Bearer token authentication middleware (`-auth-token`) for hosted or containerized MCP server deployments.

---

## Quickstart

### 1. Build or fetch `libmoonshine` and setup models

Ensure `libmoonshine.{dylib,so}` is available and point `MOONSHINE_LIB_DIR` at it:

```sh
cd ../.. # repo root
export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"

# Download STT model (if not already downloaded):
./bin/moonshine setup --language en_us --arch tiny
```

### 2. Build the sample

```sh
cd samples/mcp-transcribe
go build ./...
```

---

## Usage Modes

### Mode A: Stdio Transport (Claude Desktop / Cursor)

Add `mcp-transcribe` to your Claude Desktop configuration (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "moonshine-stt": {
      "command": "/path/to/samples/mcp-transcribe/mcp-transcribe",
      "args": ["-transport", "stdio"],
      "env": {
        "MOONSHINE_LIB_DIR": "/path/to/moonshine-go/.moonshine/lib"
      }
    }
  }
}
```

### Mode B: Hosted Streamable HTTP Transport

Run `mcp-transcribe` as a hostable HTTP server with an optional bearer token:

```sh
./mcp-transcribe -transport http -port 8080 -auth-token secret123
```

Test the HTTP endpoint using `curl`:

```sh
# Unauthorized request (returns 401):
curl -i http://localhost:8080/

# Authorized request:
curl -i -H "Authorization: Bearer secret123" http://localhost:8080/
```

---

## Tool Schema

### `transcribe`

Transcribes a local `.wav` audio file in-process and returns structured lines with timestamps and timing metrics.

#### Parameters

| Parameter | Type | Required | Description |
|---|---|:---:|---|
| `path` | `string` | **Yes** | Absolute or relative path to local `.wav` audio file |
| `language` | `string` | No | STT model language tag (default: `en_us`) |
| `arch` | `string` | No | STT model architecture (`tiny`, `base`, `tiny-streaming`; default: `tiny`) |
| `word_timestamps` | `boolean` | No | Include per-word timing summaries |
| `identify_speakers` | `boolean` | No | Enable speaker diarization (`S0`, `S1`) |

#### Example Response Output

```json
{
  "language": "en_us",
  "arch": "tiny",
  "lines": [
    {
      "start_time": 0.0,
      "duration": 2.5,
      "speaker": "",
      "text": "Hello world, transcribing audio locally with Moonshine.",
      "mean_confidence": 0.96
    }
  ],
  "stats": {
    "audio_duration_sec": 2.5,
    "inference_ms": 42.0,
    "real_time_factor": 59.5
  },
  "note": "Audio is transcribed 100% locally in-process via pkg/moonshine. No raw audio data is uploaded or transmitted to external servers."
}
```
