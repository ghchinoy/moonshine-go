#!/usr/bin/env bash
# scripts/fetch-bench-assets.sh -- fetch benchmark test audio into bench/testdata/
set -euo pipefail

DEST_DIR="bench/testdata"
mkdir -p "${DEST_DIR}"

TWO_CITIES_FILE="${DEST_DIR}/two_cities_16k.wav"
BECKETT_FILE="${DEST_DIR}/beckett.wav"

# Source locations
LOCAL_SRC="${MOONSHINE_SRC:-${HOME}/projects/github/moonshine}"
REMOTE_BASE="https://raw.githubusercontent.com/moonshine-ai/moonshine/main"

fetch_file() {
  local target_path="$1"
  local local_rel="$2"
  local remote_rel="$3"

  if [ -f "${target_path}" ]; then
    echo "✓ ${target_path} already present ($(du -h "${target_path}" | cut -f1))"
    return 0
  fi

  if [ -f "${LOCAL_SRC}/${local_rel}" ]; then
    echo "Copying ${target_path} from local checkout (${LOCAL_SRC}/${local_rel})..."
    cp "${LOCAL_SRC}/${local_rel}" "${target_path}"
    return 0
  fi

  echo "Downloading ${target_path} from ${REMOTE_BASE}/${remote_rel}..."
  curl -sSL "${REMOTE_BASE}/${remote_rel}" -o "${target_path}"
}

echo "Fetching benchmark test audio into ${DEST_DIR}..."
fetch_file "${TWO_CITIES_FILE}" "test-assets/two_cities_16k.wav" "test-assets/two_cities_16k.wav"
fetch_file "${BECKETT_FILE}" "language-bindings/python/src/moonshine_voice/assets/beckett.wav" "language-bindings/python/src/moonshine_voice/assets/beckett.wav"

echo "Done. Test audio assets ready in ${DEST_DIR}/:"
ls -lh "${DEST_DIR}"/*.wav
