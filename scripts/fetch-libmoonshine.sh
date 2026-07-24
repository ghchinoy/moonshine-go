#!/usr/bin/env bash
# Downloads prebuilt libmoonshine and onnxruntime shared libraries from a
# moonshine-ai/moonshine GitHub release, staging them into .moonshine/lib/
# so the Go bindings (internal/moonshine) can dlopen them at runtime.
#
# Usage:
#   ./scripts/fetch-libmoonshine.sh [tag] [platform]
#
#   tag:      release tag (default: contents of MOONSHINE_RELEASE_TAG file)
#   platform: macos-arm64 | linux-x86_64 | linux-arm64 | windows-x86_64
#             (default: auto-detected from uname)
#
# Output:
#   .moonshine/lib/libmoonshine.{so,dylib}
#   .moonshine/lib/libonnxruntime*
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

TAG_FILE="${REPO_ROOT}/MOONSHINE_RELEASE_TAG"
DEFAULT_TAG="v0.0.73"

if [[ -f "${TAG_FILE}" ]]; then
  DEFAULT_TAG="$(tr -d '[:space:]' < "${TAG_FILE}")"
fi

RAW_TAG="${1:-}"
PLATFORM_ARG="${2:-}"

# If first argument is a platform name (e.g. linux-x86_64), treat it as platform and use default tag
if [[ "${RAW_TAG}" == macos-* || "${RAW_TAG}" == linux-* || "${RAW_TAG}" == windows-* ]]; then
  PLATFORM_ARG="${RAW_TAG}"
  RAW_TAG=""
fi

TAG="${RAW_TAG:-${MOONSHINE_TAG:-${DEFAULT_TAG}}}"

if [[ -z "${PLATFORM_ARG}" ]]; then
  UNAME_S="$(uname -s)"
  UNAME_M="$(uname -m)"
  case "${UNAME_S}-${UNAME_M}" in
    Darwin-arm64)  PLATFORM_ARG="macos-arm64" ;;
    Darwin-x86_64) PLATFORM_ARG="macos-x86_64" ;;
    Linux-x86_64)  PLATFORM_ARG="linux-x86_64" ;;
    Linux-aarch64|Linux-arm64) PLATFORM_ARG="linux-arm64" ;;
    MINGW*|MSYS*|CYGWIN*) PLATFORM_ARG="windows-x86_64" ;;
    *)
      echo "error: unrecognized system ${UNAME_S}-${UNAME_M}; pass platform explicitly" >&2
      exit 1
      ;;
  esac
fi

PLATFORM="${PLATFORM_ARG}"
REPO="moonshine-ai/moonshine"
ASSET="moonshine-voice-${PLATFORM}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"
OUT_DIR="${MOONSHINE_LIB_OUT:-${REPO_ROOT}/.moonshine/lib}"

echo "==> Fetching prebuilt ${ASSET} (${TAG})"
echo "==> ${URL}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

if ! curl -sSLf -o "${WORK_DIR}/${ASSET}" "${URL}"; then
  echo "error: could not download ${ASSET} for ${TAG}." >&2
  echo "  verify tag '${TAG}' and platform '${PLATFORM}' exist on https://github.com/${REPO}/releases" >&2
  exit 1
fi

tar -xzf "${WORK_DIR}/${ASSET}" -C "${WORK_DIR}"
LIB_DIR="$(find "${WORK_DIR}" -type d -name lib | head -n1)"

if [[ -z "${LIB_DIR}" ]]; then
  echo "error: no lib/ directory found in extracted archive" >&2
  exit 1
fi

SHARED_LIB=""
for candidate in libmoonshine.so libmoonshine.dylib moonshine.dll; do
  if [[ -f "${LIB_DIR}/${candidate}" ]]; then
    SHARED_LIB="${LIB_DIR}/${candidate}"
    break
  fi
done

if [[ -z "${SHARED_LIB}" ]]; then
  echo "error: prebuilt asset ${ASSET} (${TAG}) contains no shared library (.so/.dylib/.dll)." >&2
  echo "  (Only static libraries were found, which cannot be loaded by purego/dlopen)." >&2
  echo "  For this platform (${PLATFORM}), build from source using:" >&2
  echo "    make buildlib MOONSHINE_SRC=/path/to/moonshine/checkout" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"

echo "==> Staging shared libraries from ${ASSET} into ${OUT_DIR}"
cp -a "${LIB_DIR}"/* "${OUT_DIR}/"

echo ""
echo "==> Successfully fetched prebuilt ${TAG} (${PLATFORM}) into ${OUT_DIR}:"
ls -la "${OUT_DIR}"
echo ""
echo "Point moonshine-go at it with:"
echo "  export MOONSHINE_LIB_DIR=\"${OUT_DIR}\""
