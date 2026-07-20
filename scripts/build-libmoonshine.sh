#!/usr/bin/env bash
# Builds libmoonshine (the shared library backing moonshine-c-api.h) from a
# local checkout of https://github.com/moonshine-ai/moonshine, and stages it
# together with its onnxruntime runtime dependency into a local output
# directory so the Go bindings (internal/moonshine) can dlopen it at runtime.
#
# Usage:
#   MOONSHINE_SRC=~/projects/github/moonshine ./scripts/build-libmoonshine.sh
#   ./scripts/build-libmoonshine.sh /path/to/moonshine/checkout
#
# Output:
#   .moonshine/lib/libmoonshine.{dylib,so}
#   .moonshine/lib/libonnxruntime.*             (staged alongside, required at runtime)
#
# Point the CLI at this directory with:
#   export MOONSHINE_LIB_DIR="$(pwd)/.moonshine/lib"
# or set `lib.dir` in the moonshine config file.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

MOONSHINE_SRC="${1:-${MOONSHINE_SRC:-}}"
if [[ -z "${MOONSHINE_SRC}" ]]; then
  for candidate in "${HOME}/projects/github/moonshine" "${HOME}/github/moonshine" "${HOME}/moonshine"; do
    if [[ -f "${candidate}/core/moonshine-c-api.h" ]]; then
      MOONSHINE_SRC="${candidate}"
      break
    fi
  done
fi
if [[ -z "${MOONSHINE_SRC}" || ! -f "${MOONSHINE_SRC}/core/moonshine-c-api.h" ]]; then
  echo "error: could not find a moonshine checkout." >&2
  echo "  pass it as an argument, or set MOONSHINE_SRC, e.g.:" >&2
  echo "  MOONSHINE_SRC=~/projects/github/moonshine $0" >&2
  exit 1
fi
echo "==> Using moonshine checkout: ${MOONSHINE_SRC}"

# Check if build-critical files are unpulled Git LFS pointers
if grep -q "https://git-lfs.github.com/spec/v1" "${MOONSHINE_SRC}/core/moonshine-tts/src/zipvoice-voices-data.cpp" 2>/dev/null; then
  echo "error: Git LFS pointer files detected in ${MOONSHINE_SRC}!" >&2
  echo "  Run 'git -C ${MOONSHINE_SRC} lfs pull' to pull required build assets and retry." >&2
  exit 1
fi

BUILD_DIR="${MOONSHINE_BUILD_DIR:-${MOONSHINE_SRC}/core/build-moonshine-go}"
OUT_DIR="${MOONSHINE_LIB_OUT:-${REPO_ROOT}/.moonshine/lib}"
JOBS="${JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)}"

mkdir -p "${OUT_DIR}"

echo "==> Configuring CMake (build dir: ${BUILD_DIR})"
cmake -S "${MOONSHINE_SRC}/core" -B "${BUILD_DIR}" \
  -DCMAKE_BUILD_TYPE=Release \
  -DMOONSHINE_TTS_BUILD_ONNX=ON

echo "==> Building target 'moonshine' (this can take a while the first time)"
cmake --build "${BUILD_DIR}" --target moonshine -j "${JOBS}"

# Locate the built shared library. Single-config generators (Makefiles/Ninja,
# the default on macOS/Linux) put it directly in the build dir.
LIB_PATH=""
for name in libmoonshine.dylib libmoonshine.so; do
  found="$(find "${BUILD_DIR}" -maxdepth 3 -name "${name}" -type f 2>/dev/null | head -n1)"
  if [[ -n "${found}" ]]; then
    LIB_PATH="${found}"
    break
  fi
done
if [[ -z "${LIB_PATH}" ]]; then
  echo "error: could not find libmoonshine.{dylib,so} under ${BUILD_DIR}" >&2
  exit 1
fi
echo "==> Built: ${LIB_PATH}"
cp "${LIB_PATH}" "${OUT_DIR}/"

# Stage the matching onnxruntime runtime library alongside it. On macOS
# libmoonshine is built with INSTALL_RPATH=@loader_path, so onnxruntime must
# sit next to it. On Linux the moonshine build sets BUILD_RPATH to the vendored
# onnxruntime dir directly, but we stage a copy anyway for a self-contained
# output directory.
UNAME_S="$(uname -s)"
UNAME_M="$(uname -m)"
ORT_DIR="${MOONSHINE_SRC}/core/third-party/onnxruntime/lib"
ORT_SRC=""
case "${UNAME_S}-${UNAME_M}" in
  Darwin-arm64)  ORT_SRC="$(find "${ORT_DIR}/macos/arm64" -name 'libonnxruntime*.dylib' 2>/dev/null | head -n1)" ;;
  Darwin-x86_64) ORT_SRC="$(find "${ORT_DIR}/macos/x86_64" -name 'libonnxruntime*.dylib' 2>/dev/null | head -n1)" ;;
  Linux-aarch64) ORT_SRC="$(find "${ORT_DIR}/linux/aarch64" -name 'libonnxruntime.so*' 2>/dev/null | head -n1)" ;;
  Linux-x86_64)  ORT_SRC="$(find "${ORT_DIR}/linux/x86_64" -name 'libonnxruntime.so*' 2>/dev/null | head -n1)" ;;
  *) echo "warning: unrecognized platform ${UNAME_S}-${UNAME_M}, skipping onnxruntime staging" >&2 ;;
esac
if [[ -n "${ORT_SRC}" ]]; then
  cp "${ORT_SRC}" "${OUT_DIR}/"
  echo "==> Staged onnxruntime: ${ORT_SRC}"
else
  echo "warning: could not locate a vendored onnxruntime library to stage" >&2
fi

echo ""
echo "==> Done. Staged in ${OUT_DIR}:"
ls -la "${OUT_DIR}"
echo ""
echo "Point moonshine-go at it with:"
echo "  export MOONSHINE_LIB_DIR=\"${OUT_DIR}\""
