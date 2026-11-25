#!/usr/bin/env bash
# ggcode installer for macOS and Linux.
#
#   curl -fsSL https://ggcode.dev/install.sh | bash
#
# Detects the OS/architecture, downloads the matching release archive,
# verifies its SHA-256 against the release's checksums.txt and installs the
# `ggcode` binary into ~/.local/bin (no sudo). Compatible with bash 3.2
# (stock macOS bash); needs curl or wget, tar, and sha256sum/shasum/openssl.
#
# Options:
#   --dry-run, -n        Print the resolved URLs and exit without downloading
#   --version <tag>      Install a specific release (e.g. v1.3.292); default: latest
#   --install-dir <dir>  Install directory (default: ~/.local/bin)
#   -h, --help           Show this help
#
# Environment overrides:
#   GGCODE_INSTALL_DIR     Install directory (default: ~/.local/bin)
#   GGCODE_VERSION         Release tag to install (default: latest)
#   GGCODE_INSTALL_DRY_RUN Set to 1 for a dry run
#   GGCODE_DOWNLOAD_BASE   Download mirror (default: https://ggcode.dev/downloads/latest;
#                          manifest.json and checksums.txt are looked up here first)
#   GGCODE_MANIFEST_URL    Explicit manifest.json URL (default: $GGCODE_DOWNLOAD_BASE/manifest.json)
#   GGCODE_CHECKSUMS_URL   Explicit checksums.txt URL (no fallback when set)
#   GGCODE_ARCHIVE_URL     Explicit archive URL (no fallback when set)

set -euo pipefail

REPO="${GGCODE_REPO:-topcheer/ggcode}"
DOWNLOAD_BASE="${GGCODE_DOWNLOAD_BASE:-https://ggcode.dev/downloads/latest}"
GITHUB_BASE="${GGCODE_GITHUB_BASE:-https://github.com/${REPO}}"
API_URL="${GGCODE_API_URL:-https://api.github.com/repos/${REPO}/releases/latest}"
INSTALL_DIR="${GGCODE_INSTALL_DIR:-${HOME:-}/.local/bin}"
VERSION="${GGCODE_VERSION:-latest}"
DRY_RUN="${GGCODE_INSTALL_DRY_RUN:-0}"
MANIFEST_URL="${GGCODE_MANIFEST_URL:-}"
CHECKSUMS_URL="${GGCODE_CHECKSUMS_URL:-}"
ARCHIVE_URL_OVERRIDE="${GGCODE_ARCHIVE_URL:-}"

BINARY_NAME="ggcode"

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  BOLD=$'\033[1m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[0;33m'; CYAN=$'\033[0;36m'; RED=$'\033[0;31m'; NC=$'\033[0m'
else
  BOLD=''; GREEN=''; YELLOW=''; CYAN=''; RED=''; NC=''
fi
info() { printf '%s\n' "${CYAN}info${NC}: $*"; }
ok()   { printf '%s\n' "${GREEN}ok${NC}: $*"; }
warn() { printf '%s\n' "${YELLOW}warn${NC}: $*" >&2; }
die()  { printf '%s\n' "${RED}error${NC}: $*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: install.sh [options]

  --dry-run, -n        Print the resolved URLs and exit without downloading
  --version <tag>      Install a specific release (e.g. v1.3.292); default: latest
  --install-dir <dir>  Install directory (default: ~/.local/bin)
  -h, --help           Show this help

Environment overrides:
  GGCODE_INSTALL_DIR, GGCODE_VERSION, GGCODE_INSTALL_DRY_RUN,
  GGCODE_DOWNLOAD_BASE, GGCODE_MANIFEST_URL, GGCODE_CHECKSUMS_URL, GGCODE_ARCHIVE_URL
EOF
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run|-n)
      DRY_RUN=1
      shift
      ;;
    --version)
      [[ $# -ge 2 ]] || die "--version requires a value"
      VERSION="$2"
      shift 2
      ;;
    --install-dir)
      [[ $# -ge 2 ]] || die "--install-dir requires a value"
      INSTALL_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      die "unknown option: $1"
      ;;
  esac
done

# ---------------------------------------------------------------------------
# Platform detection
# ---------------------------------------------------------------------------
OS="$(uname -s)"
ARCH="$(uname -m)"
case "${OS}" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux" ;;
  *) die "unsupported operating system: ${OS}. ggcode ships for macOS and Linux." ;;
esac
case "${ARCH}" in
  x86_64|amd64) ARCH="x86_64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) die "unsupported architecture: ${ARCH}. ggcode ships for x86_64 and arm64." ;;
esac
ARCHIVE_NAME="ggcode_${OS}_${ARCH}.tar.gz"

if [[ -z "${INSTALL_DIR}" || "${INSTALL_DIR}" == "/.local/bin" ]]; then
  die "HOME is not set; pass --install-dir <dir> or set GGCODE_INSTALL_DIR"
fi

# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------
USE_CURL=0
if command -v curl >/dev/null 2>&1; then
  USE_CURL=1
elif ! command -v wget >/dev/null 2>&1; then
  die "neither curl nor wget is installed"
fi
command -v tar >/dev/null 2>&1 || die "tar is required to extract the archive"
command -v awk >/dev/null 2>&1 || die "awk is required"

CURL_FLAGS="-fsSL"
if [[ -t 1 ]]; then
  # Show a progress bar when stdout is a terminal (curl | bash keeps stdout).
  CURL_FLAGS="-fL --progress-bar"
fi

fetch_to_file() { # fetch_to_file <url> <dest>
  if [[ "${USE_CURL}" == "1" ]]; then
    # shellcheck disable=SC2086
    curl ${CURL_FLAGS} --retry 3 --retry-delay 2 --connect-timeout 15 -o "$2" "$1"
  else
    wget -q --tries=3 --timeout=30 -O "$2" "$1"
  fi
}

fetch_to_stdout() { # fetch_to_stdout <url>
  if [[ "${USE_CURL}" == "1" ]]; then
    curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 "$1"
  else
    wget -q --tries=3 --timeout=30 -O- "$1"
  fi
}

sha256_of() { # sha256_of <file>
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    die "need sha256sum, shasum or openssl to verify the download"
  fi
}

# download_first <dest> <url>...: try each URL in order; sets DOWNLOADED_URL.
DOWNLOADED_URL=""
download_first() {
  local dest="$1"
  shift
  local url
  for url in "$@"; do
    info "Downloading ${url}"
    rm -f "${dest}"
    if fetch_to_file "${url}" "${dest}" 2>/dev/null; then
      DOWNLOADED_URL="${url}"
      return 0
    fi
  done
  return 1
}

# ---------------------------------------------------------------------------
# Release resolution: mirror manifest -> GitHub redirect -> GitHub API
# ---------------------------------------------------------------------------
normalize_tag() {
  local v="${1#v}"
  v="${v#V}"
  [[ "${v}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || die "invalid version: $1 (expected e.g. v1.3.292)"
  printf 'v%s' "${v}"
}

if [[ -z "${MANIFEST_URL}" ]]; then
  MANIFEST_URL="${DOWNLOAD_BASE%/}/manifest.json"
fi

MANIFEST=""
TAG=""
if [[ "${VERSION}" != "latest" ]]; then
  TAG="$(normalize_tag "${VERSION}")"
else
  # 1) Download mirror manifest ({"version": "...", "files": [...]}).
  if MANIFEST="$(fetch_to_stdout "${MANIFEST_URL}" 2>/dev/null || true)" && [[ -n "${MANIFEST}" ]]; then
    TAG="$(printf '%s\n' "${MANIFEST}" | sed -nE 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' | head -1 || true)"
    [[ -n "${TAG}" ]] || warn "manifest ${MANIFEST_URL} has no version field; falling back to GitHub"
  fi
  # 2) https://github.com/<owner>/<repo>/releases/latest redirects to .../releases/tag/<tag>.
  if [[ -z "${TAG}" ]]; then
    redirect=""
    if [[ "${USE_CURL}" == "1" ]]; then
      redirect="$(curl -fsSI -o /dev/null -w '%{redirect_url}' --connect-timeout 15 "${GITHUB_BASE}/releases/latest" 2>/dev/null || true)"
    else
      redirect="$(wget --server-response --spider --max-redirect=0 --timeout=30 "${GITHUB_BASE}/releases/latest" 2>&1 >/dev/null | awk '/^[[:space:]]*Location:/{print $2}' | head -1 || true)"
    fi
    TAG="${redirect##*/}"
  fi
  # 3) GitHub REST API.
  if [[ -z "${TAG}" ]]; then
    api="$(fetch_to_stdout "${API_URL}" 2>/dev/null || true)"
    TAG="$(printf '%s\n' "${api}" | sed -nE 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' | head -1 || true)"
  fi
fi
[[ -n "${TAG}" ]] || die "could not determine the latest release (tried ${MANIFEST_URL}, ${GITHUB_BASE}/releases/latest and ${API_URL})"
[[ "${TAG}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+ ]] || die "unexpected release tag: ${TAG}"

# ---------------------------------------------------------------------------
# Resolve the archive and checksums URLs
# ---------------------------------------------------------------------------
GITHUB_ARCHIVE_URL="${GITHUB_BASE}/releases/download/${TAG}/${ARCHIVE_NAME}"
ARCHIVE_URL="${ARCHIVE_URL_OVERRIDE}"
if [[ -z "${ARCHIVE_URL}" && -n "${MANIFEST}" ]]; then
  # The manifest carries per-asset URLs; use the entry for this archive if present.
  escaped="${ARCHIVE_NAME//./\\.}"
  ARCHIVE_URL="$(printf '%s\n' "${MANIFEST}" \
    | grep -E "\"name\"[[:space:]]*:[[:space:]]*\"${escaped}\"" -A1 \
    | sed -nE 's/.*"url"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' | head -1 || true)"
fi
[[ -n "${ARCHIVE_URL}" ]] || ARCHIVE_URL="${GITHUB_ARCHIVE_URL}"

GITHUB_CHECKSUMS_URL="${GITHUB_BASE}/releases/download/${TAG}/checksums.txt"
if [[ -n "${CHECKSUMS_URL}" ]]; then
  CHECKSUM_URLS=("${CHECKSUMS_URL}")
elif [[ "${VERSION}" != "latest" ]]; then
  # The mirror only mirrors "latest"; a pinned release has its own checksums.txt.
  CHECKSUM_URLS=("${GITHUB_CHECKSUMS_URL}")
else
  CHECKSUM_URLS=("${DOWNLOAD_BASE%/}/checksums.txt" "${GITHUB_CHECKSUMS_URL}")
fi

info "Platform: ${BOLD}${OS}/${ARCH}${NC}"
info "Version: ${BOLD}${TAG}${NC}"

if [[ "${DRY_RUN}" == "1" ]]; then
  printf '%s\n' "${CYAN}info${NC}: archive:   ${ARCHIVE_URL}"
  printf '%s\n' "${CYAN}info${NC}: checksums: ${CHECKSUM_URLS[0]}"
  if [[ ${#CHECKSUM_URLS[@]} -gt 1 ]]; then
    printf '%s\n' "${CYAN}info${NC}:            (fallback: ${CHECKSUM_URLS[1]})"
  fi
  printf '%s\n' "${CYAN}info${NC}: install:   ${INSTALL_DIR%/}/${BINARY_NAME}"
  ok "dry run - nothing was downloaded or installed"
  exit 0
fi

# ---------------------------------------------------------------------------
# Download + verify
# ---------------------------------------------------------------------------
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ggcode-install.XXXXXX")"
trap 'rm -rf "${WORK_DIR}"' EXIT
ARCHIVE_PATH="${WORK_DIR}/${ARCHIVE_NAME}"
CHECKSUMS_PATH="${WORK_DIR}/checksums.txt"

if ! download_first "${ARCHIVE_PATH}" "${ARCHIVE_URL}"; then
  die "download failed: ${ARCHIVE_URL}. Check your connection, or download ${GITHUB_ARCHIVE_URL} manually."
fi
[[ -s "${ARCHIVE_PATH}" ]] || die "downloaded archive is empty: ${ARCHIVE_URL}"

if ! download_first "${CHECKSUMS_PATH}" "${CHECKSUM_URLS[@]}"; then
  die "could not download checksums.txt from: ${CHECKSUM_URLS[*]}"
fi
CHECKSUMS_SOURCE="${DOWNLOADED_URL}"
[[ -s "${CHECKSUMS_PATH}" ]] || die "checksums file is empty: ${CHECKSUMS_SOURCE}"

expected="$(tr -d '\r' < "${CHECKSUMS_PATH}" \
  | grep -E "^[0-9a-fA-F]{64}[[:space:]]+\*?${ARCHIVE_NAME//./\\.}\$" \
  | head -1 | awk '{print $1}' || true)"
expected="$(printf '%s' "${expected}" | tr 'A-F' 'a-f')"
[[ -n "${expected}" ]] || die "no SHA-256 entry for ${ARCHIVE_NAME} in ${CHECKSUMS_SOURCE}"

actual="$(sha256_of "${ARCHIVE_PATH}")"
actual="$(printf '%s' "${actual}" | tr 'A-F' 'a-f')"
if [[ "${actual}" != "${expected}" ]]; then
  die "checksum mismatch for ${ARCHIVE_NAME}: expected ${expected}, got ${actual}. Refusing to install."
fi
ok "Checksum verified (${ARCHIVE_NAME})"

# ---------------------------------------------------------------------------
# Extract + install
# ---------------------------------------------------------------------------
info "Extracting..."
tar -xzf "${ARCHIVE_PATH}" -C "${WORK_DIR}"
BINARY_PATH="$(find "${WORK_DIR}" -type f -name "${BINARY_NAME}" | head -1 || true)"
[[ -n "${BINARY_PATH}" ]] || die "could not find ${BINARY_NAME} in ${ARCHIVE_NAME}"

mkdir -p "${INSTALL_DIR}"
INSTALL_TARGET="${INSTALL_DIR%/}/${BINARY_NAME}"
# Install through a temp file so a running ggcode is replaced atomically.
cp "${BINARY_PATH}" "${INSTALL_TARGET}.tmp.$$"
chmod 755 "${INSTALL_TARGET}.tmp.$$"
mv -f "${INSTALL_TARGET}.tmp.$$" "${INSTALL_TARGET}"
ok "Installed ${BINARY_NAME} ${TAG} to ${INSTALL_TARGET}"

case ":${PATH:-}:" in
  *":${INSTALL_DIR%/}:"*) ;;
  *)
    warn "${INSTALL_DIR} is not in your PATH. Add it to your shell profile:"
    # shellcheck disable=SC2016
    printf '  export PATH="%s:$PATH"\n' "${INSTALL_DIR%/}" >&2
    ;;
esac

if VERSION_OUTPUT="$("${INSTALL_TARGET}" --version 2>&1)"; then
  ok "ggcode is ready: ${VERSION_OUTPUT}"
  printf '\n  Run %s to start (open a new terminal first if PATH was updated).\n' "${BOLD}ggcode${NC}"
else
  warn "installed, but '${INSTALL_TARGET} --version' failed:"
  printf '%s\n' "${VERSION_OUTPUT:-}" >&2
  exit 1
fi

exit 0
