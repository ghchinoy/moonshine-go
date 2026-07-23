#!/usr/bin/env bash
# Packages a built moonshine CLI binary and staged .moonshine/lib/ shared
# libraries into a self-contained release tarball in dist/.
#
# Usage:
#   ./scripts/package-release.sh [platform]
#
# Output:
#   dist/moonshine-<version>-<platform>.tar.gz
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

PLATFORM_ARG="${1:-}"

if [[ -z "${PLATFORM_ARG}" ]]; then
  UNAME_S="$(uname -s)"
  UNAME_M="$(uname -m)"
  case "${UNAME_S}-${UNAME_M}" in
    Darwin-arm64)  PLATFORM_ARG="macos-arm64" ;;
    Darwin-x86_64) PLATFORM_ARG="macos-x86_64" ;;
    Linux-x86_64)  PLATFORM_ARG="linux-x86_64" ;;
    Linux-aarch64|Linux-arm64) PLATFORM_ARG="linux-arm64" ;;
    *) PLATFORM_ARG="unknown-${UNAME_S}-${UNAME_M}" ;;
  esac
fi

PLATFORM="${PLATFORM_ARG}"
VERSION="${VERSION:-$(git -C "${REPO_ROOT}" describe --tags --always --dirty 2>/dev/null || echo dev)}"

BIN_PATH="${REPO_ROOT}/bin/moonshine"
LIB_DIR="${REPO_ROOT}/.moonshine/lib"
DIST_DIR="${REPO_ROOT}/dist"

if [[ ! -f "${BIN_PATH}" ]]; then
  echo "==> Building bin/moonshine..."
  make -C "${REPO_ROOT}" build
fi

if [[ ! -d "${LIB_DIR}" || -z "$(find "${LIB_DIR}" -name 'libmoonshine*' -o -name 'moonshine*' 2>/dev/null)" ]]; then
  echo "error: no shared libraries found in ${LIB_DIR}." >&2
  echo "  Run 'make fetchlib' (Linux) or 'make buildlib' (macOS/source) first." >&2
  exit 1
fi

mkdir -p "${DIST_DIR}"

PACKAGE_NAME="moonshine-${VERSION}-${PLATFORM}"
STAGING_DIR="$(mktemp -d)"
trap 'rm -rf "${STAGING_DIR}"' EXIT

TARGET_DIR="${STAGING_DIR}/${PACKAGE_NAME}"
mkdir -p "${TARGET_DIR}/bin"
mkdir -p "${TARGET_DIR}/lib"

cp "${BIN_PATH}" "${TARGET_DIR}/bin/"
cp -a "${LIB_DIR}"/* "${TARGET_DIR}/lib/"

if [[ -f "${REPO_ROOT}/LICENSE" ]]; then
  cp "${REPO_ROOT}/LICENSE" "${TARGET_DIR}/"
fi
if [[ -f "${REPO_ROOT}/README.md" ]]; then
  cp "${REPO_ROOT}/README.md" "${TARGET_DIR}/"
fi

# Write a wrapper launcher script in the root of the archive for convenience
cat << 'EOF' > "${TARGET_DIR}/run.sh"
#!/usr/bin/env bash
# Helper script to launch moonshine with MOONSHINE_LIB_DIR pointing to the bundled lib/ directory.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export MOONSHINE_LIB_DIR="${DIR}/lib"
exec "${DIR}/bin/moonshine" "$@"
EOF
chmod +x "${TARGET_DIR}/run.sh"

TARBALL="${DIST_DIR}/${PACKAGE_NAME}.tar.gz"
tar -czf "${TARBALL}" -C "${STAGING_DIR}" "${PACKAGE_NAME}"

echo "==> Package created: ${TARBALL}"
ls -lh "${TARBALL}"
