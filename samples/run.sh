#!/usr/bin/env bash
# Shared single-command quickstart launcher for moonshine-go samples.
# Verifies prerequisites, starts `moonshine serve` in the background with the
# sample's required flags if not already running, launches the sample, and
# cleans up background processes on exit.
#
# Usage:
#   ./samples/run.sh <sample-name> [sample-args...]
#   ./samples/run.sh --list
set -euo pipefail

SAMPLES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SAMPLES_DIR}/.." && pwd)"
MOONSHINE_BIN="${REPO_ROOT}/bin/moonshine"

export MOONSHINE_LIB_DIR="${MOONSHINE_LIB_DIR:-${REPO_ROOT}/.moonshine/lib}"

BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${BLUE}[samples/run.sh]${NC} $*"; }
ok()    { echo -e "${GREEN}[samples/run.sh]${NC} $*"; }
warn()  { echo -e "${YELLOW}[samples/run.sh]${NC} $*"; }
err()   { echo -e "${RED}[samples/run.sh]${NC} $*" >&2; }

list_samples() {
  cat <<'EOF'
Available moonshine-go samples:

  Tier 0 (Read-Only Subscriptions):
    go-listen                Go WebSocket transcript listener
    grpc-listen              Go gRPC stream transcript listener
    python-listen            Python WebSocket transcript listener
    go-stream-audio          Go bidirectional 16kHz PCM audio streamer

  Tier 1 (External Agents & Browser Clients):
    browser-listen           Browser Web Audio mic -> remote moonshine serve
    browser-cascade-faq      Browser voice FAQ agent with Web Audio TTS
    go-cascade-faq           Go AgentFlow offline voice FAQ agent
    python-agent             Python rule-based voice command agent

  Tier 2 (Go Extensions & Native In-Process):
    voice-tmux               Voice-controlled tmux terminal bridge
    voice-mcp-assistant      Gemini 3.5 Flash-Lite + stdio MCP voice assistant
    go-domain-customization  Runtime keyterm & context passage biasing
    go-bulk-analysis         Faster-than-real-time batch STT & Gemini analysis
    go-embedded              In-process pure-Go STT & streaming TTS (no daemon)
    mcp-transcribe           Standalone stdio/HTTP MCP server for local STT
    desktop-app              Native Wails v2 desktop GUI application

Usage:
  ./samples/run.sh <sample-name> [sample-args...]
EOF
}

if [[ $# -eq 0 || "$1" == "-h" || "$1" == "--help" || "$1" == "--list" ]]; then
  list_samples
  exit 0
fi

SAMPLE="$1"
shift
SAMPLE_DIR="${SAMPLES_DIR}/${SAMPLE}"

if [[ ! -d "${SAMPLE_DIR}" ]]; then
  err "Unknown sample '${SAMPLE}'."
  echo ""
  list_samples
  exit 1
fi

ensure_moonshine_bin() {
  if [[ ! -x "${MOONSHINE_BIN}" ]]; then
    info "Building ${MOONSHINE_BIN}..."
    (cd "${REPO_ROOT}" && make build)
  fi
}

port_open() {
  local port="$1"
  nc -z 127.0.0.1 "${port}" >/dev/null 2>&1
}

SERVE_PID=""
cleanup() {
  if [[ -n "${SERVE_PID}" ]] && kill -0 "${SERVE_PID}" >/dev/null 2>&1; then
    info "Stopping background moonshine serve (PID ${SERVE_PID})..."
    kill "${SERVE_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

start_sidecar_if_needed() {
  local port="$1"
  shift
  if port_open "${port}"; then
    ok "Detected existing server on port ${port}; reusing it."
    return 0
  fi
  ensure_moonshine_bin
  info "Starting background moonshine serve ($*)..."
  "${MOONSHINE_BIN}" serve "$@" &
  SERVE_PID=$!
  for _ in {1..30}; do
    if port_open "${port}"; then
      ok "moonshine serve ready on port ${port} (PID ${SERVE_PID})."
      return 0
    fi
    if ! kill -0 "${SERVE_PID}" >/dev/null 2>&1; then
      err "moonshine serve exited prematurely."
      exit 1
    fi
    sleep 0.2
  done
  err "Timed out waiting for moonshine serve on port ${port}."
  exit 1
}

cd "${SAMPLE_DIR}"

case "${SAMPLE}" in
  go-listen)
    start_sidecar_if_needed 8765 --transport ws
    info "Launching ${SAMPLE}..."
    go run . "$@"
    ;;
  grpc-listen)
    start_sidecar_if_needed 9090 --transport grpc
    info "Launching ${SAMPLE}..."
    go run . "$@"
    ;;
  python-listen)
    start_sidecar_if_needed 8765 --transport ws
    info "Launching ${SAMPLE}..."
    python3 listen.py "$@"
    ;;
  python-agent)
    start_sidecar_if_needed 8765 --transport ws --allow-actions --agent external
    info "Launching ${SAMPLE}..."
    python3 agent.py "$@"
    ;;
  go-stream-audio)
    start_sidecar_if_needed 8765 --transport ws --audio-source remote --remote-audio-encoding int16 --remote-audio-rate 16000 --remote-audio-channels 1
    info "Launching ${SAMPLE}..."
    go run . "$@"
    ;;
  go-cascade-faq|go-domain-customization|voice-tmux|voice-mcp-assistant)
    start_sidecar_if_needed 8765 --transport ws --allow-actions --agent external
    info "Launching ${SAMPLE}..."
    go run . "$@"
    ;;
  browser-listen)
    start_sidecar_if_needed 8765 --transport ws --audio-source remote --remote-audio-encoding int16 --remote-audio-rate 16000 --remote-audio-channels 1
    info "Serving ${SAMPLE} at http://localhost:8080 (Ctrl-C to stop)..."
    python3 -m http.server 8080
    ;;
  browser-cascade-faq)
    start_sidecar_if_needed 8765 --transport ws --audio-source remote --remote-audio-encoding int16 --remote-audio-rate 16000 --remote-audio-channels 1 --allow-actions --agent external
    info "Serving ${SAMPLE} at http://localhost:8080 (Ctrl-C to stop)..."
    python3 -m http.server 8080
    ;;
  go-embedded|mcp-transcribe|go-bulk-analysis|desktop-app)
    ensure_moonshine_bin
    info "Launching in-process/CLI sample ${SAMPLE}..."
    go run . "$@"
    ;;
  *)
    err "No launch rule configured for ${SAMPLE}"
    exit 1
    ;;
esac
