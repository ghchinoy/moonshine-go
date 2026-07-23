# Testing Linux behavior locally with Apple's `container`

If you're on an Apple Silicon Mac, you can verify Linux-specific behavior --
notably, whether a fetched `libmoonshine.so` release asset actually
`dlopen()`s cleanly -- without installing Docker Desktop. macOS ships (or
lets you install) [`container`](https://github.com/apple/container), Apple's
native containerization CLI: it runs real Linux VMs via the same
lightweight virtualization framework as other container runtimes, speaks
the OCI image format, and needs no separate daemon.

This isn't a Docker replacement for this project's actual container image
work (see bd issue `moonshine-go-d15` for the mic-less `moonshine serve`
container) -- it's a fast local loop for **verifying native library
behavior on Linux before you trust it**, which is exactly the gap that
caught a real bug (below).

---

## Why this matters here specifically

`internal/moonshine` dlopens `libmoonshine.{so,dylib}` at runtime via
[`ebitengine/purego`](https://github.com/ebitengine/purego) --
no cgo needed to *build* the binding, but the actual runtime behavior
(symbol resolution, shared-library dependency search paths, glibc symbol
versioning) can only be verified by *actually loading the library on the
target OS*. Static inspection (`scripts/check-release-asset.sh`'s
`otool`/`strings`-based checks) catches a lot -- missing shared libraries,
un-bundled dependencies, absolute build-machine RPATHs -- but it does **not**
catch every class of portability problem.

**Case in point:** the `v0.0.71` `moonshine-voice-linux-x86_64.tar.gz`
release asset passed every static check in
`scripts/check-release-asset.sh` (shared library present, `libonnxruntime`
bundled, portable `$ORIGIN` rpath) -- and then failed to `dlopen()` on a
real Ubuntu 22.04/24.04 container with:

```
OSError: /lib/x86_64-linux-gnu/libm.so.6: version `GLIBC_2.43' not found (required by ./libmoonshine.so)
```

GLIBC 2.43 doesn't exist in any current major Linux distribution --
upstream's x86_64 build environment was apparently built against an
unusually new glibc. The `linux-arm64` asset, built and tested the same
way, required only GLIBC 2.27 (portable back to Ubuntu 18.04+) and loaded
without issue. This regression is tracked as `moonshine-go-dh7` and reported upstream as
[moonshine-ai/moonshine#206](https://github.com/moonshine-ai/moonshine/issues/206);
it's the motivating example for this doc, and the reason a live `dlopen()`
test is worth the extra few minutes beyond the static checks.

---

## Install / check for `container`

```sh
container --version
```

If it's not installed, see [Apple's `container` releases](https://github.com/apple/container/releases)
or `brew install --cask container` (availability depends on your macOS
version -- `container` requires a sufficiently recent macOS with the
Containerization framework).

Make sure the system services are running:

```sh
container system status
```

---

## Native vs. emulated architecture

Apple Silicon runs Linux **arm64** containers natively (fast, no
emulation). Testing an **x86_64** asset requires `--arch amd64`, which runs
under emulation (noticeably slower, but functionally correct enough to
catch real bugs -- it's how the GLIBC issue above was found).

```sh
# Native arm64 -- fast.
container run -d --name moonshine-test ubuntu:22.04 sleep 3600

# Emulated x86_64 -- slower, but tests the actual target architecture.
container run -d --name moonshine-test-x64 --arch amd64 ubuntu:22.04 sleep 3600
```

Prefer testing **both** architectures you ship prebuilt assets for
(`linux-x86_64` and `linux-arm64`) rather than assuming one implies the
other -- as the GLIBC case shows, they can have materially different
portability characteristics even from the same upstream release.

---

## The dlopen() test recipe

1. **Start a container and install a minimal toolchain** (the base `ubuntu`
   images don't include `curl`/`python3`):

   ```sh
   container exec moonshine-test bash -c \
     "apt-get update -qq && apt-get install -y -qq curl python3 ca-certificates"
   ```

2. **Fetch the release asset you want to verify** (matching what
   `scripts/fetch-libmoonshine.sh` / `make fetchlib` would pull):

   ```sh
   container exec moonshine-test bash -c '
     cd /tmp
     curl -sL -o mv.tar.gz \
       https://github.com/moonshine-ai/moonshine/releases/download/v0.0.71/moonshine-voice-linux-arm64.tar.gz
     tar -xzf mv.tar.gz
   '
   ```

   Or, to test a library you already staged locally (e.g. via `make
   fetchlib`), copy it in instead of re-downloading:

   ```sh
   container cp "$(pwd)/.moonshine/lib/libmoonshine.so" moonshine-test:/tmp/libmoonshine.so
   container cp "$(pwd)/.moonshine/lib/libonnxruntime.so.1" moonshine-test:/tmp/libonnxruntime.so.1
   ```

   (`container cp` needs an absolute source path -- a bare relative path
   fails with a confusing "source not found" error.)

3. **Actually `dlopen()` it.** Deliberately unset `LD_LIBRARY_PATH` first --
   the whole point is verifying the library resolves its own dependencies
   (e.g. `libonnxruntime.so.1`) via its baked-in `$ORIGIN` rpath, the same
   way purego's `dlopen()` call will in production, with no environment
   help:

   ```sh
   container exec moonshine-test bash -c '
     cd /tmp/moonshine-voice-linux-arm64/lib   # or /tmp if you used container cp
     unset LD_LIBRARY_PATH
     python3 -c "
   import ctypes
   lib = ctypes.CDLL(\"./libmoonshine.so\")
   lib.moonshine_get_version.restype = ctypes.c_int32
   print(\"version:\", lib.moonshine_get_version())
   "
   '
   ```

   A clean `version: 20000` (or whatever the current
   `internal/moonshine.HeaderVersion` is) confirms the library loads and
   resolves its dependencies correctly -- no `LD_LIBRARY_PATH`, no missing
   symbol errors. This is functionally equivalent to what
   `internal/moonshine.Load()` does at runtime via purego.

4. **Clean up:**

   ```sh
   container stop moonshine-test moonshine-test-x64
   container rm moonshine-test moonshine-test-x64
   ```

---

## Going further: testing the actual Go binary

The Python `ctypes` test above verifies the *library*. To test moonshine-go
itself end-to-end inside a container, cross-compile a Linux binary and copy
it in alongside the fetched library -- note `internal/audio`'s mic capture
(`gen2brain/malgo`) requires cgo, so a `CGO_ENABLED=0` cross-compile will
fail to build the full CLI; a smaller test program that only calls
`moonshine.Load(path)` and `moonshine.LoadTranscriber(...)` avoids that
dependency and is enough to confirm the binding layer works end-to-end
against a container-verified library.

---

## See also

- [RELEASING.md](RELEASING.md) -- the release pipeline this verification
  workflow supports; `scripts/check-release-asset.sh` is the static
  equivalent of the live test in this doc.
- [hosting.md](hosting.md) -- deployment shapes for `moonshine serve`,
  including the mic-less container image this workflow helps validate.
