# Releasing moonshine-go

This document describes how `moonshine-go` is versioned, packaged, and released.

---

## Release Architecture

`moonshine-go` combines two version concepts:

1. **CLI / Repo Version (`vX.Y.Z`)**: The semver tag of `moonshine-go` itself (e.g. `v0.3.0`), set in `cmd/moonshine/version.go` at build time via `-ldflags`.
2. **Upstream Library Pin (`MOONSHINE_RELEASE_TAG`)**: The version of `libmoonshine` (e.g. `v0.0.73`) that prebuilt binary assets are fetched from.

---

## Platform Support Matrix

| Platform | Prebuilt Assets | Release Mechanism |
| :--- | :--- | :--- |
| **Linux (x86_64)** | ✅ Supported (fixed upstream in `v0.0.73`) | Automated via GitHub Actions (`make fetchlib`) |
| **Linux (arm64)** | ✅ Supported | Automated via GitHub Actions (`make fetchlib`) |
| **macOS (arm64)** | ⏳ Pending upstream `hbq` | Built from source via `make buildlib` |
| **Windows (x86_64)**| ⏳ Pending upstream `hbq` | Built from source via `make buildlib` |

*(Note: macOS and Windows upstream release assets currently ship static libraries only. They are tracked under task `moonshine-go-hbq` and will be added to automated releases once upstream exports shared libraries).*

**Note (`moonshine-go-dh7` resolved):** the `linux-x86_64` release asset for
`v0.0.71` required GLIBC 2.43, which failed to `dlopen()` on standard Linux distros.
This was reported upstream ([moonshine-ai/moonshine#206](https://github.com/moonshine-ai/moonshine/issues/206))
and fixed in `v0.0.73`. Both `linux-x86_64` and `linux-arm64` assets are now fully supported.

---

## How to Cut a Release

### 1. Pre-flight Checks

Ensure all unit tests and quality gates pass locally:

```sh
make fmt
make vet
make test
```

Verify that `MOONSHINE_RELEASE_TAG` contains the desired upstream version pin (e.g. `v0.0.73`) and passes asset validation:

```sh
scripts/check-release-asset.sh linux-x86_64 $(cat MOONSHINE_RELEASE_TAG)
scripts/check-release-asset.sh linux-arm64 $(cat MOONSHINE_RELEASE_TAG)
```

**Static checks are not sufficient on their own** -- as `moonshine-go-dh7`
showed, a glibc-version mismatch can pass every static check and still fail
to `dlopen()` at runtime. Also do a live load test before cutting a
release (see [testing-with-container.md](testing-with-container.md) if
you're on Apple Silicon without Docker):

```sh
make fetchlib MOONSHINE_PLATFORM=linux-x86_64
# then dlopen()-test .moonshine/lib/libmoonshine.so on an actual Linux host
# or container -- see testing-with-container.md for the exact recipe.
```

### 2. Tag and Push

To publish a new release, create an annotated Git tag and push it to GitHub:

```sh
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

### 3. Automated CI Pipeline

Pushing a `v*` tag triggers the GitHub Actions workflow in `.github/workflows/release.yml`:

1. Fetches prebuilt `libmoonshine.so` + `libonnxruntime.so.1` for the pinned upstream tag.
2. Compiles `bin/moonshine` with `-ldflags "-X main.version=v0.1.0"`.
3. Packages a self-contained archive `dist/moonshine-v0.1.0-linux-x86_64.tar.gz`.
4. Creates a new GitHub Release for `v0.1.0` and attaches the tarball asset.

---

## Local / Manual Release Packaging

To build and package a release archive manually on your local machine:

```sh
# 1. Fetch native shared libraries
make fetchlib MOONSHINE_PLATFORM=linux-x86_64

# 2. Build CLI binary
make build

# 3. Create dist/ archive
make release-package MOONSHINE_PLATFORM=linux-x86_64
```

The resulting package lives in `dist/moonshine-<version>-<platform>.tar.gz` containing:

```
moonshine-v0.1.0-linux-x86_64/
├── bin/
│   └── moonshine
├── lib/
│   ├── libmoonshine.so
│   └── libonnxruntime.so.1
├── run.sh
├── LICENSE
└── README.md
```

Users can extract the archive and run `./run.sh` or set `MOONSHINE_LIB_DIR=./lib ./bin/moonshine`.
