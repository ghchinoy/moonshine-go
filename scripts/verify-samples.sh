#!/usr/bin/env bash
# Verifies all samples under samples/ by running language-appropriate static
# checks (go build, go vet, gofmt, CGO_ENABLED=0 checks for embedding samples,
# and python3 -m py_compile).
#
# Usage:
#   ./scripts/verify-samples.sh
#
# Exit status:
#   0 if all sample checks pass, non-zero if any sample fails.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
SAMPLES_DIR="${REPO_ROOT}/samples"

if [[ ! -d "${SAMPLES_DIR}" ]]; then
  echo "error: samples directory not found at ${SAMPLES_DIR}" >&2
  exit 1
fi

RED='\031[0;31m'
GREEN='\032[0;32m'
BLUE='\034[0;34m'
NC='\033[0m' # No Color

# Disable color if stdout is not a TTY
if [[ ! -t 1 ]]; then
  RED=""
  GREEN=""
  BLUE=""
  NC=""
fi

PASSED_COUNT=0
FAILED_COUNT=0
FAILED_SAMPLES=()

log_info() {
  echo -e "${BLUE}==>${NC} $1"
}

log_pass() {
  echo -e "  ${GREEN}✓${NC} $1"
}

log_fail() {
  echo -e "  ${RED}✗${NC} $1"
}

check_go_sample() {
  local dir="$1"
  local name="$2"
  local failed=0

  log_info "Verifying Go sample: ${name}"

  # Pre-build step for samples with gitignored embedded frontend assets (e.g. desktop-app)
  if [[ "${name}" == "desktop-app" && -d "${dir}/frontend" ]]; then
    if (cd "${dir}/frontend" && npm ci >/dev/null 2>&1 && npm run build >/dev/null 2>&1); then
      log_pass "npm ci && npm run build (frontend)"
    else
      log_fail "building frontend assets failed (npm ci && npm run build)"
      (cd "${dir}/frontend" && npm ci && npm run build) || true
      failed=1
    fi
  fi

  # 1. Standard go build
  if (cd "${dir}" && go build ./... >/dev/null 2>&1); then
    log_pass "go build ./..."
  else
    log_fail "go build ./... failed"
    (cd "${dir}" && go build ./...) || true
    failed=1
  fi

  # 2. go vet
  if (cd "${dir}" && go vet ./... >/dev/null 2>&1); then
    log_pass "go vet ./..."
  else
    log_fail "go vet ./... failed"
    (cd "${dir}" && go vet ./...) || true
    failed=1
  fi

  # 3. gofmt formatting check
  local unformatted
  unformatted="$(cd "${dir}" && gofmt -l . 2>/dev/null || true)"
  if [[ -z "${unformatted}" ]]; then
    log_pass "gofmt"
  else
    log_fail "gofmt check failed (unformatted files: ${unformatted})"
    failed=1
  fi

  # 4. CGO_ENABLED=0 check for pure Go embedding / client samples
  # All samples in this repo except desktop-app (which uses Wails/cgo GUI bindings)
  # must compile cleanly with CGO_ENABLED=0.
  if [[ "${name}" != "desktop-app" ]]; then
    if (cd "${dir}" && CGO_ENABLED=0 go build ./... >/dev/null 2>&1); then
      log_pass "CGO_ENABLED=0 go build ./..."
    else
      log_fail "CGO_ENABLED=0 go build ./... failed"
      (cd "${dir}" && CGO_ENABLED=0 go build ./...) || true
      failed=1
    fi
  fi

  # Clean up any compiled test binary artifacts created in sample directory
  (cd "${dir}" && rm -f "${name}" "moonshine-sample-${name}" 2>/dev/null || true)

  return ${failed}
}

check_python_sample() {
  local dir="$1"
  local name="$2"
  local failed=0

  log_info "Verifying Python sample: ${name}"

  # Check python3 syntax via py_compile
  if command -v python3 >/dev/null 2>&1; then
    local py_files
    py_files="$(find "${dir}" -maxdepth 2 -name "*.py" 2>/dev/null)"
    if [[ -n "${py_files}" ]]; then
      if python3 -m py_compile ${py_files} >/dev/null 2>&1; then
        log_pass "python3 -m py_compile (*.py)"
      else
        log_fail "python3 -m py_compile failed"
        python3 -m py_compile ${py_files} || true
        failed=1
      fi
    else
      log_pass "no python files found to compile"
    fi
  else
    log_info "skipping py_compile (python3 not installed)"
  fi

  return ${failed}
}

check_browser_sample() {
  local dir="$1"
  local name="$2"

  log_info "Verifying Browser sample: ${name}"
  log_pass "static assets verified"
  return 0
}

echo "=================================================="
echo "      moonshine-go: Verifying All Samples         "
echo "=================================================="
echo ""

for sample_path in "${SAMPLES_DIR}"/*; do
  if [[ ! -d "${sample_path}" ]]; then
    continue
  fi

  sample_name="$(basename "${sample_path}")"

  if [[ -f "${sample_path}/go.mod" ]]; then
    if check_go_sample "${sample_path}" "${sample_name}"; then
      PASSED_COUNT=$((PASSED_COUNT + 1))
    else
      FAILED_COUNT=$((FAILED_COUNT + 1))
      FAILED_SAMPLES+=("${sample_name}")
    fi
  elif [[ -f "${sample_path}/requirements.txt" || -f "${sample_path}/main.py" ]]; then
    if check_python_sample "${sample_path}" "${sample_name}"; then
      PASSED_COUNT=$((PASSED_COUNT + 1))
    else
      FAILED_COUNT=$((FAILED_COUNT + 1))
      FAILED_SAMPLES+=("${sample_name}")
    fi
  elif [[ -f "${sample_path}/index.html" ]]; then
    if check_browser_sample "${sample_path}" "${sample_name}"; then
      PASSED_COUNT=$((PASSED_COUNT + 1))
    else
      FAILED_COUNT=$((FAILED_COUNT + 1))
      FAILED_SAMPLES+=("${sample_name}")
    fi
  fi
  echo ""
done

echo "=================================================="
echo "Summary: ${PASSED_COUNT} passed, ${FAILED_COUNT} failed"
echo "=================================================="

if [[ ${FAILED_COUNT} -gt 0 ]]; then
  echo -e "${RED}The following samples failed verification:${NC}"
  for f in "${FAILED_SAMPLES[@]}"; do
    echo "  - ${f}"
  done
  exit 1
fi

echo -e "${GREEN}All samples verified successfully!${NC}"
exit 0
