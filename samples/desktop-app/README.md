# samples/desktop-app — Native Desktop Voice App via Wails & `pkg/moonshine`

In-process Speech-to-Text desktop application built with [Wails v2](https://wails.io) (Go + HTML/JS webview) and [`pkg/moonshine`](../../pkg/moonshine).

This sample demonstrates building a standalone, self-contained desktop GUI application that embeds Moonshine STT directly in Go memory space without running a `moonshine serve` sidecar daemon.

## Sample Rating

| Axis | Rating / Details |
|---|---|
| **Tier** | Native / in-process (no daemon) |
| **Complexity** | 3/5 |
| **Setup Cost** | High (requires Wails v2 CLI, Node.js/npm, C compiler/Cocoa/WebKit, `libmoonshine.{dylib,so}`, and STT model) |
| **Pillars** | Privacy, Composability |
| **Industry / Use Case** | Native Desktop GUI Apps, Dictation Overlays, Offline Voice Tools |
| **Appeal** | 5/5 |

---

## Architecture: Webview Mic Capture + In-Process Go STT

Unlike daemon IPC samples (`browser-listen`, `go-cascade-faq`) that stream raw audio over WebSocket to a separate background server, `desktop-app` runs entirely within a single desktop application process:

```
[ Wails Webview Frontend ]                       [ Go Application Backend ]
navigator.mediaDevices.getUserMedia          wails.Run() / App struct
  └─► AudioWorklet (16kHz PCM)                    └─► pkg/moonshine (purego)
        └─► App.PushPCMChunk(floatArray) ───────►       └─► Stream.AddAudio()
                                                              └─► Stream.Transcribe()
```

1. **Frontend Audio Capture** — the HTML/JS webview frontend captures microphone audio using `navigator.mediaDevices.getUserMedia` and buffers 100ms 16kHz float32 PCM chunks via an `AudioWorklet`.
2. **In-Process IPC** — chunks are sent to the Go backend via Wails' auto-generated JS-to-Go method bindings (`App.PushPCMChunk`).
3. **Embedded STT** — the Go backend feeds the PCM samples directly into `pkg/moonshine.Stream` and returns interim/finalized transcript lines back to the UI.

> **Design Callout (Bridge Overhead vs Simplicity):** Wails' JS-to-Go bridge marshals Go method parameters via JSON. For 100ms audio chunks (~1600 float32 numbers), passing raw float arrays is simple and zero-dependency, incurring minimal overhead at 10Hz call cadence.

---

## Prerequisites

1. **Go 1.23+** and **Node.js (18+) / npm**.
2. **Wails CLI (v2)**:
   ```sh
   go install github.com/wailsapp/wails/v2/cmd/wails@latest
   ```
3. **`libmoonshine` Shared Library**:
   Point `MOONSHINE_LIB_DIR` at your local `libmoonshine.{dylib,so}` directory:
   ```sh
   export MOONSHINE_LIB_DIR="/path/to/moonshine-go/.moonshine/lib"
   ```
4. **STT Model**:
   ```sh
   ./bin/moonshine setup --language en_us --arch tiny-streaming
   ```

---

## Running in Development Mode

Launch the live-reloading desktop development window:

```sh
cd samples/desktop-app
wails dev
```

## Building a Standalone Application Bundle

Package a production native desktop application bundle (`.app` on macOS, `.exe` on Windows):

```sh
wails build
```

The compiled binary and application bundle will be created under `build/bin/`.
