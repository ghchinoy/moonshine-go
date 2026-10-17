#!/usr/bin/env bash
# Checks for upstream drift between moonshine-go's pinned MOONSHINE_RELEASE_TAG
# and upstream moonshine-ai/moonshine releases, tags, branches, and C API headers.
#
# Usage:
#   scripts/check-upstream.sh [target_ref]
#
# If target_ref is omitted, checks against the newest upstream release tag and
# active development branch (e.g. dev-v*).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

UPSTREAM_REPO="moonshine-ai/moonshine"
PINNED_TAG_FILE="${REPO_ROOT}/MOONSHINE_RELEASE_TAG"

if [[ ! -f "${PINNED_TAG_FILE}" ]]; then
  echo "error: ${PINNED_TAG_FILE} not found" >&2
  exit 1
fi

PINNED_TAG="$(tr -d '[:space:]' < "${PINNED_TAG_FILE}")"
TARGET_REF="${1:-}"

echo "=================================================================="
echo " Upstream Synchronization & Drift Inspector"
echo " Pinned Release Tag:  ${PINNED_TAG}"
echo " Upstream Repository: https://github.com/${UPSTREAM_REPO}"
echo "=================================================================="
echo ""

# Check prerequisites
if ! command -v gh >/dev/null 2>&1; then
  echo "warning: GitHub CLI ('gh') not found. Using git remote inspection."
  USE_GH=0
else
  USE_GH=1
fi

# 1. Discover latest tags and branches
echo "==> Querying upstream refs..."
REMOTE_TAGS="$(git ls-remote --tags "https://github.com/${UPSTREAM_REPO}.git" | grep -v '\^{}' | awk '{print $2}' | sed 's@refs/tags/@@' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+' || true)"
LATEST_TAG="$(echo "${REMOTE_TAGS}" | sort -V | tail -n 1)"

DEV_BRANCH="$(git ls-remote --heads "https://github.com/${UPSTREAM_REPO}.git" | awk '{print $2}' | sed 's@refs/heads/@@' | grep -E '^dev-v' | sort -V | tail -n 1 || true)"
if [[ -z "${DEV_BRANCH}" ]]; then
  DEV_BRANCH="main"
fi

COMPARE_REF="${TARGET_REF:-${LATEST_TAG}}"
if [[ -z "${COMPARE_REF}" ]]; then
  COMPARE_REF="${DEV_BRANCH}"
fi

echo "  Pinned tag:         ${PINNED_TAG}"
echo "  Latest remote tag:  ${LATEST_TAG:-none}"
echo "  Active dev branch:  ${DEV_BRANCH}"
echo "  Comparing against:  ${COMPARE_REF}"
echo ""

# 2. Check if pinned tag matches latest
if [[ "${PINNED_TAG}" == "${LATEST_TAG}" ]]; then
  echo "✓ Pinned tag matches latest upstream release tag (${PINNED_TAG})."
else
  echo "! Pinned tag (${PINNED_TAG}) differs from latest upstream tag (${LATEST_TAG})."
fi
echo ""

# 3. Check GitHub Release & Prebuilt Assets for COMPARE_REF
echo "==> Checking release publication and prebuilt assets for ${COMPARE_REF}..."
if [[ "${USE_GH}" -eq 1 ]]; then
  REL_JSON="$(gh api "repos/${UPSTREAM_REPO}/releases/tags/${COMPARE_REF}" 2>/dev/null || true)"
  if echo "${REL_JSON}" | jq -e '.id != null' >/dev/null 2>&1; then
    DRAFT="$(echo "${REL_JSON}" | jq -r .draft)"
    PRERELEASE="$(echo "${REL_JSON}" | jq -r .prerelease)"
    PUB_AT="$(echo "${REL_JSON}" | jq -r .published_at)"
    ASSET_COUNT="$(echo "${REL_JSON}" | jq -r '.assets | length')"
    echo "✓ GitHub Release exists for ${COMPARE_REF} (published: ${PUB_AT}, assets: ${ASSET_COUNT}, draft: ${DRAFT}, prerelease: ${PRERELEASE})"
    echo "  Platforms checked:"
    for p in linux-x86_64 macos-arm64; do
      if echo "${REL_JSON}" | jq -e --arg name "moonshine-voice-${p}.tar.gz" '.assets[] | select(.name == $name)' >/dev/null 2>&1; then
        echo "    ✓ ${p}: asset present"
      else
        echo "    ✗ ${p}: asset missing"
      fi
    done
  else
    echo "✗ No published GitHub Release found for ${COMPARE_REF} (HTTP 404 / tag exists but release/assets not published)."
    echo "  Note: Do NOT bump MOONSHINE_RELEASE_TAG until release assets are uploaded!"
  fi
else
  echo "(install 'gh' to automatically verify release asset payloads)"
fi
echo ""

# 4. Compare commits & inspect C API header diff
echo "==> Comparing ${PINNED_TAG}...${COMPARE_REF}..."
if [[ "${USE_GH}" -eq 1 ]]; then
  COMPARE_DATA="$(gh api "repos/${UPSTREAM_REPO}/compare/${PINNED_TAG}...${COMPARE_REF}" 2>/dev/null || true)"
  if [[ -n "${COMPARE_DATA}" ]]; then
    AHEAD_BY="$(echo "${COMPARE_DATA}" | jq -r .ahead_by)"
    BEHIND_BY="$(echo "${COMPARE_DATA}" | jq -r .behind_by)"
    echo "  Ahead by: ${AHEAD_BY} commits, Behind by: ${BEHIND_BY} commits"
    echo ""
    echo "  Recent upstream commits:"
    echo "${COMPARE_DATA}" | jq -r '.commits[0:10][] | "    • " + (.sha[0:8]) + " " + ((.commit.message | split("\n"))[0])'
    echo ""
    
    # Check if core/moonshine-c-api.h was touched
    C_API_TOUCHED="$(echo "${COMPARE_DATA}" | jq -r '.files[] | select(.filename == "core/moonshine-c-api.h") | .filename')"
    if [[ -n "${C_API_TOUCHED}" ]]; then
      echo "  ⚠️  ALERT: core/moonshine-c-api.h HAS CHANGED between ${PINNED_TAG} and ${COMPARE_REF}!"
      echo "      Header diff requires reviewing pkg/moonshine C ABI bindings & MOONSHINE_HEADER_VERSION."
    else
      echo "  ✓ core/moonshine-c-api.h has NOT changed (C ABI and HEADER_VERSION 30000 remain compatible)."
    fi

    # Check for core/ files changed
    CORE_FILES="$(echo "${COMPARE_DATA}" | jq -r '.files[] | select(.filename | startswith("core/")) | .filename')"
    if [[ -n "${CORE_FILES}" ]]; then
      echo ""
      echo "  Upstream core/ files modified:"
      echo "${CORE_FILES}" | sed 's/^/    • /'
    fi
  else
    echo "  (Could not fetch commit comparison via GitHub API)"
  fi
fi
echo ""

# 5. Check local clone if present
LOCAL_SRC="${MOONSHINE_SRC:-${HOME}/projects/github/moonshine}"
if [[ -d "${LOCAL_SRC}/.git" ]]; then
  echo "==> Local Moonshine clone detected at ${LOCAL_SRC}:"
  CURRENT_BRANCH="$(git -C "${LOCAL_SRC}" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "detached")"
  CURRENT_COMMIT="$(git -C "${LOCAL_SRC}" rev-parse --short HEAD 2>/dev/null || echo "unknown")"
  echo "  Current branch: ${CURRENT_BRANCH} (${CURRENT_COMMIT})"
  DIRTY="$(git -C "${LOCAL_SRC}" status --porcelain 2>/dev/null || true)"
  if [[ -n "${DIRTY}" ]]; then
    echo "  Local worktree has uncommitted modifications."
  else
    echo "  Local worktree is clean."
  fi
else
  echo "(Local moonshine source checkout not found at ${LOCAL_SRC}; export MOONSHINE_SRC to point to one)"
fi

echo ""
echo "=================================================================="
echo " Upstream Synchronization Check Completed"
echo "=================================================================="
