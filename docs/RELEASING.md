# Releasing moonshine-go

This document describes how `moonshine-go` is versioned, packaged, and released.

---

## Release Architecture

`moonshine-go` combines two version concepts:

1. **CLI / Repo Version (`vX.Y.Z`)**: The semver tag of `moonshine-go` itself (e.g. `v0.1.0`), set in `cmd/moonshine/version.go` at build time via `-ldflags`.
2. **Upstream Library Pin (`MOONSHINE_RELEASE_TAG`)**: The version of `libmoonshine` (e.g. `v0.0.71`) that prebuilt binary assets are fetched from.

---

## Platform Support Matrix

| Platform | Prebuilt Assets | Release Mechanism |
| :--- | :--- | :--- |
| **Linux (x86_64)** | ⚠️ Broken as of `v0.0.71` (see below) | Automated via GitHub Actions (`make fetchlib`) |
| **Linux (arm64)** | ✅ Supported | Automated via GitHub Actions (`make fetchlib`) |
| **macOS (arm64)** | ⏳ Pending upstream `hbq` | Built from source via `make buildlib` |
| **Windows (x86_64)**| ⏳ Pending upstream `hbq` | Built from source via `make buildlib` |

*(Note: macOS and Windows upstream release assets currently ship static libraries only. They are tracked under task `moonshine-go-hbq` and will be added to automated releases once upstream exports shared libraries).*

**⚠️ Known issue (`moonshine-go-dh7`):** the `linux-x86_64` release asset for
`v0.0.71` requires GLIBC 2.43, which does not exist in current Ubuntu LTS
releases (22.04 is 2.35, 24.04 is 2.39) or most other current distros --
`scripts/check-release-asset.sh`'s static checks all pass, but the library
fails to `dlopen()` at runtime. This was found via a live load test (see
[testing-with-container.md](testing-with-container.md)); `.github/workflows/release.yml`
currently has no step that would catch this before publishing a release.
Reported upstream: [moonshine-ai/moonshine#206](https://github.com/moonshine-ai/moonshine/issues/206).
The `linux-arm64` asset is unaffected (requires only GLIBC 2.27) and is
safe to rely on today.

---

## How to Cut a Release

### 1. Pre-flight Checks

Ensure all unit tests and quality gates pass locally:

```sh
make fmt
make vet
make test
```

Verify that `MOONSHINE_RELEASE_TAG` contains the desired upstream version pin (e.g. `v0.0.71`) and passes asset validation:

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
