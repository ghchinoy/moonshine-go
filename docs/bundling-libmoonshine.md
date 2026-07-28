# Bundling and Distributing `libmoonshine`

This guide explains how to package and distribute native `libmoonshine` shared libraries alongside Go applications built with [`pkg/moonshine`](../pkg/moonshine) or [`pkg/serveapi`](../pkg/serveapi).

---

## Overview & Architecture

[`pkg/moonshine`](../pkg/moonshine) is a pure-Go package (`CGO_ENABLED=0`) that binds to `libmoonshine`'s C ABI at runtime using [`ebitengine/purego`](https://github.com/ebitengine/purego). 

Because `pkg/moonshine` does not use cgo, Go applications compile cleanly without a C toolchain (GCC/Clang/MSVC) on the build host. However, at **runtime**, the application must locate and `dlopen` (or `LoadLibrary` on Windows) `libmoonshine` and its bundled dependency `libonnxruntime`.

### Asset Separation

Production applications manage two distinct categories of assets:

1. **Native Libraries (Shared Objects)**: Platform-specific binary libraries (`libmoonshine.{dylib,so}` / `moonshine.dll` and `libonnxruntime.{dylib,so,dll}`). These are bundled inside the app installer, macOS `.app` bundle, or container image.
2. **Model Weights**: ONNX neural network weights (`encoder_model.ort`, `decoder_model.ort`, etc.) downloaded on demand into a cache directory (default: `~/.cache/moonshine_voice` or `%LOCALAPPDATA%\moonshine_voice`), or bundled in offline installations.

---

## Runtime Library Resolution

When your application calls `moonshine.Load(path)`, `pkg/moonshine.ResolveLibPath` searches for `libmoonshine` in the following order:

1. **Explicit Override (`path`)**: Direct path to file or directory passed to `moonshine.Load(path)`.
2. **`MOONSHINE_LIB_PATH`**: Environment variable specifying exact path to the shared library file.
3. **`MOONSHINE_LIB_DIR`**: Environment variable specifying a directory containing the library.
4. **Local Build Directory**: `./.moonshine/lib` relative to current working directory.
5. **System Library Paths**: `/usr/local/lib`, `/opt/homebrew/lib`.

### Executable-Relative Resolution Pattern

For production desktop or CLI applications where environment variables should not be required from end users, use `os.Executable()` to compute the library path relative to the running binary before calling `moonshine.Load()`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/moonshine-ai/moonshine-go/pkg/moonshine"
)

func initNativeLibrary() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting executable path: %w", err)
	}
	execDir := filepath.Dir(execPath)

	// Resolve native library adjacent to binary or inside app bundle
	libDir := filepath.Join(execDir, "lib") // or Contents/Frameworks on macOS
	if err := moonshine.Load(libDir); err != nil {
		return fmt.Errorf("loading libmoonshine: %w", err)
	}
	return nil
}
```

---

## Platform Packaging Recipes

### 1. macOS Desktop Apps (Wails / Native GUI)

On macOS, place `libmoonshine.dylib` and `libonnxruntime.dylib` inside the application bundle's `Contents/Frameworks/` directory.

#### Bundle Directory Layout

```text
MyApp.app/
└── Contents/
    ├── MacOS/
    │   └── MyApp (executable)
    ├── Frameworks/
    │   ├── libmoonshine.dylib
    │   └── libonnxruntime.dylib
    └── Resources/
        └── icon.icns
```

#### Runtime Code Resolution

```go
func resolveMacOSLibraryPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return ""
	}
	// MacOS binary lives at MyApp.app/Contents/MacOS/MyApp
	// Frameworks live at MyApp.app/Contents/Frameworks/
	contentsDir := filepath.Dir(filepath.Dir(execPath))
	frameworksDir := filepath.Join(contentsDir, "Frameworks")
	return frameworksDir
}
```

#### Code-Signing & Notarization (Inside-Out Order)

Apple Gatekeeper requires all bundled native libraries to be signed before or alongside the main application executable. Always sign in **inside-out** order:

1. **Sign Frameworks First**:
   ```sh
   codesign --force --options runtime \
     --sign "Developer ID Application: Your Name (TEAMID)" \
     MyApp.app/Contents/Frameworks/libonnxruntime.dylib

   codesign --force --options runtime \
     --sign "Developer ID Application: Your Name (TEAMID)" \
     MyApp.app/Contents/Frameworks/libmoonshine.dylib
   ```

2. **Sign Application Bundle**:
   ```sh
   codesign --force --deep --options runtime \
     --entitlements entitlements.plist \
     --sign "Developer ID Application: Your Name (TEAMID)" \
     MyApp.app
   ```

3. **Notarize Application**:
   ```sh
   ditto -c -k --keepParent MyApp.app MyApp.zip
   xcrun notarytool submit MyApp.zip \
     --keychain-profile "AC_PASSWORD" \
     --wait
   xcrun stapler staple MyApp.app
   ```

---

### 2. Windows Applications

On Windows, place `moonshine.dll` and `onnxruntime.dll` in the same directory as your `.exe` file.

#### Directory Layout

```text
C:\Program Files\MyApp\
├── MyApp.exe
├── moonshine.dll
└── onnxruntime.dll
```

When `MyApp.exe` runs, `LoadLibrary` automatically resolves `onnxruntime.dll` side-by-side from the executable directory when `moonshine.dll` is loaded.

#### Visual C++ Runtime Requirement

`libmoonshine` on Windows depends on the Universal C Runtime (UCRT). Ensure target systems have the [Microsoft Visual C++ Redistributable](https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist) installed, or bundle `vcruntime140.dll` / `msvcp140.dll` if distributing a portable zip.

---

### 3. Linux Applications & Container Images

On Linux, distribute `libmoonshine.so` and `libonnxruntime.so.1` alongside your binary or in standard library paths.

#### Container / Docker Packaging

Use a multi-stage Dockerfile that fetches or builds `libmoonshine` and stages it in `/usr/local/lib`:

```dockerfile
# Stage 1: Runtime base with dependencies
FROM ubuntu:22.04 AS runner
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    alsa-utils \
    libasound2 \
    && rm -rf /var/lib/apt/lists/*

# Copy prebuilt shared libraries to system library path
COPY lib/libmoonshine.so /usr/local/lib/
COPY lib/libonnxruntime.so.1 /usr/local/lib/
RUN ldconfig

# Copy compiled Go application
COPY bin/my-voice-app /usr/local/bin/

ENV MOONSHINE_LIB_DIR=/usr/local/lib
ENTRYPOINT ["/usr/local/bin/my-voice-app"]
```

---

## MCP Server Packaging

When packaging an MCP (Model Context Protocol) server (such as [`samples/mcp-transcribe`](../samples/mcp-transcribe/)):

### 1. Stdio Transport

MCP stdio servers communicate via JSON-RPC over `stdout`. **All library logging must be isolated to `stderr` or suppressed** so `stdout` streams remain pure JSON:

- Ensure `pkg/moonshine` or custom logger writes initialization messages to `os.Stderr`.
- Place `libmoonshine.{dylib,so}` adjacent to the server binary or resolve via `MOONSHINE_LIB_DIR` in the MCP client config (e.g. `claude_desktop_config.json`).

Example Claude Desktop configuration:

```json
{
  "mcpServers": {
    "moonshine-transcribe": {
      "command": "/usr/local/bin/mcp-transcribe",
      "env": {
        "MOONSHINE_LIB_DIR": "/usr/local/lib/moonshine"
      }
    }
  }
}
```

### 2. Streamable HTTP Transport

For remote or containerized MCP hosts, serve over HTTP (`mcp.StreamableHTTPHandler`). The native libraries are packaged inside the server container image using standard Linux container practices.

---

## Model Asset Management

To keep binary installers small, download models dynamically on first run using `pkg/moonshine.DownloadModel`:

```go
package main

import (
	"context"
	"fmt"
	"github.com/moonshine-ai/moonshine-go/pkg/moonshine"
)

func ensureModelAssets(ctx context.Context) (string, error) {
	// Downloads "moonshine/tiny" for "en" into ~/.cache/moonshine_voice if not present
	modelDir, err := moonshine.DownloadModel(ctx, "en", moonshine.ArchBase)
	if err != nil {
		return "", fmt.Errorf("downloading model: %w", err)
	}
	return modelDir, nil
}
```

For completely offline/air-gapped deployments, package the model directory (`encoder_model.ort`, `decoder_model.ort`, `tokenizer.json`) directly into the installer resources or container image and pass its path to `moonshine.LoadTranscriber(modelDir, arch)`.
