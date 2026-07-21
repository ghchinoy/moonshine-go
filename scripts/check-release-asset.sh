#!/usr/bin/env bash
# Checks whether a moonshine-ai/moonshine GitHub release's prebuilt
# moonshine-voice-<platform>.tar.gz asset is actually usable by purego-based
# dynamic bindings (like this project's internal/moonshine), without needing
# to build libmoonshine from source.
#
# This automates the manual investigation that first found the packaging
# gaps in the v0.0.69 release (see moonshine-go-related bd issues for full
# context): a shared library (not just a static .a/.lib) is required, its
# runtime dependency on libonnxruntime must be either statically linked or
# bundled in the same archive, and any dynamic-library search path baked
# into the binary must be portable (relative to the library's own location,
# e.g. @loader_path/$ORIGIN) rather than an absolute path on the upstream
# maintainer's build machine.
#
# Usage:
#   scripts/check-release-asset.sh <platform> [tag]
#
#   platform: macos-arm64 | linux-x86_64 | windows-x86_64 | rpi-arm64
#             (must match the moonshine-voice-<platform>.tar.gz asset name)
#   tag:      a moonshine-ai/moonshine release tag, e.g. v0.0.69
#             (default: whatever `gh release view` resolves as latest)
#
# Requires: curl, tar, and (optionally) `gh` to resolve the latest tag when
# one isn't given explicitly. On macOS, uses `otool -L` for dependency/rpath
# inspection; on other platforms falls back to a `strings`-based heuristic
# (looking for NEEDED entries and suspicious absolute paths) since readelf/
# patchelf aren't assumed to be installed.
set -euo pipefail

PLATFORM="${1:?usage: $0 <platform> [tag]}"
TAG="${2:-}"
REPO="moonshine-ai/moonshine"

if [[ -z "${TAG}" ]]; then
  if command -v gh >/dev/null 2>&1; then
    TAG="$(gh release view --repo "${REPO}" --json tagName --jq .tagName)"
  else
    echo "error: no tag given and 'gh' is not installed to resolve the latest one" >&2
    exit 1
  fi
fi

ASSET="moonshine-voice-${PLATFORM}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

echo "==> Checking ${ASSET} from ${TAG}"
echo "==> ${URL}"
if ! curl -sSLf -o "${WORK_DIR}/${ASSET}" "${URL}"; then
  echo "FAIL: could not download ${ASSET} for ${TAG} -- asset may not exist yet for this platform" >&2
  exit 1
fi

tar -xzf "${WORK_DIR}/${ASSET}" -C "${WORK_DIR}"
LIB_DIR="$(find "${WORK_DIR}" -type d -name lib | head -n1)"
if [[ -z "${LIB_DIR}" ]]; then
  echo "FAIL: no lib/ directory found in the archive" >&2
  exit 1
fi

echo ""
echo "--- Archive contents (lib/) ---"
ls -la "${LIB_DIR}"
echo ""

STATUS=0

check() {
  local desc="$1" ok="$2"
  if [[ "${ok}" == "1" ]]; then
    echo "PASS: ${desc}"
  else
    echo "FAIL: ${desc}"
    STATUS=1
  fi
}

SHARED_LIB=""
for candidate in libmoonshine.dylib libmoonshine.so moonshine.dll; do
  if [[ -f "${LIB_DIR}/${candidate}" ]]; then
    SHARED_LIB="${LIB_DIR}/${candidate}"
    break
  fi
done

if [[ -n "${SHARED_LIB}" ]]; then
  check "shared library present ($(basename "${SHARED_LIB}"))" 1
else
  check "shared library present (only static .a/.lib found -- unusable by purego/dlopen)" 0
  echo ""
  echo "==> Nothing further to check without a shared library. Exiting."
  exit "${STATUS}"
fi

echo ""
echo "--- Dependency / RPATH inspection ---"
case "${SHARED_LIB}" in
  *.dylib)
    OTOOL_OUT="$(otool -L "${SHARED_LIB}" 2>/dev/null || true)"
    echo "${OTOOL_OUT}"
    if echo "${OTOOL_OUT}" | grep -qi "onnxruntime"; then
      NEEDS_ORT=1
    else
      NEEDS_ORT=0
    fi
    if [[ "${NEEDS_ORT}" == "1" ]]; then
      check "libonnxruntime bundled alongside libmoonshine.dylib in the archive" \
        "$([[ -n "$(find "${LIB_DIR}" -iname 'libonnxruntime*' 2>/dev/null)" ]] && echo 1 || echo 0)"
      check "onnxruntime dependency uses a portable path (@rpath/@loader_path, not an absolute build-machine path)" \
        "$(echo "${OTOOL_OUT}" | grep -i onnxruntime | grep -qE '@rpath|@loader_path' && echo 1 || echo 0)"
    else
      echo "(libmoonshine.dylib does not appear to depend on onnxruntime dynamically -- statically linked?)"
    fi
    ;;
  *.so)
    if command -v readelf >/dev/null 2>&1; then
      READELF_OUT="$(readelf -d "${SHARED_LIB}" 2>/dev/null || true)"
      echo "${READELF_OUT}"
      NEEDS_ORT="$(echo "${READELF_OUT}" | grep -qi "onnxruntime" && echo 1 || echo 0)"
      RPATH_LINE="$(echo "${READELF_OUT}" | grep -E 'RPATH|RUNPATH' || true)"
    else
      # No readelf (e.g. macOS without a cross toolchain) -- fall back to a
      # strings-based heuristic. Not as precise, but catches the two
      # concrete issues found in practice: a missing/absolute dependency.
      STRINGS_OUT="$(strings "${SHARED_LIB}" | grep -i onnxruntime || true)"
      echo "${STRINGS_OUT}"
      NEEDS_ORT="$([[ -n "${STRINGS_OUT}" ]] && echo 1 || echo 0)"
      RPATH_LINE="$(echo "${STRINGS_OUT}" | grep -E '^/(home|Users)/' || true)"
    fi
    if [[ "${NEEDS_ORT}" == "1" ]]; then
      check "libonnxruntime bundled alongside libmoonshine.so in the archive" \
        "$([[ -n "$(find "${LIB_DIR}" -iname 'libonnxruntime*' 2>/dev/null)" ]] && echo 1 || echo 0)"
      check "no absolute build-machine path found in onnxruntime search path (heuristic: readelf RPATH/RUNPATH or strings scan)" \
        "$([[ -z "${RPATH_LINE}" ]] && echo 1 || echo 0)"
      if [[ -n "${RPATH_LINE}" ]]; then
        echo "  found: ${RPATH_LINE}"
      fi
    else
      echo "(libmoonshine.so does not appear to depend on onnxruntime dynamically -- statically linked?)"
    fi
    ;;
  *.dll)
    echo "(no automated dependency check implemented for .dll yet -- inspect manually, e.g. with dumpbin /dependents on Windows)"
    check "onnxruntime.dll present alongside moonshine.dll in the archive" \
      "$([[ -n "$(find "${LIB_DIR}" -iname 'onnxruntime.dll' 2>/dev/null)" ]] && echo 1 || echo 0)"
    ;;
esac

echo ""
if [[ "${STATUS}" == "0" ]]; then
  echo "==> ${ASSET} (${TAG}) looks usable as a prebuilt asset for purego-based bindings."
else
  echo "==> ${ASSET} (${TAG}) is NOT yet usable as-is -- see FAIL lines above."
fi
exit "${STATUS}"
