#!/usr/bin/env bash
# ggcode interactive installer for macOS and Linux
# Usage:
#   curl -fsSL https://ggcode.dev/install.sh | bash         # interactive (default: user)
#   curl -fsSL https://ggcode.dev/install.sh | bash -s -- --system   # non-interactive system
#   curl -fsSL https://ggcode.dev/install.sh | bash -s -- --user     # non-interactive user
set -euo pipefail

REPO="topcheer/ggcode"
OWNER="topcheer"
BINARY_NAME="ggcode"
API_URL="https://api.github.com/repos/${REPO}/releases/latest"

# Colors
if [[ -t 1 ]]; then
  BOLD='\033[1m'
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  CYAN='\033[0;36m'
  RED='\033[0;31m'
  NC='\033[0m'
else
  BOLD=''; GREEN=''; YELLOW=''; CYAN=''; RED=''; NC=''
fi

info()  { echo -e "${CYAN}info${NC}: $*"; }
warn()  { echo -e "${YELLOW}warn${NC}: $*"; }
error() { echo -e "${RED}error${NC}: $*" >&2; }
ok()    { echo -e "${GREEN}ok${NC}: $*"; }

# --- Determine scope ---
SCOPE="user"
SYSTEM_FLAG=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --system|--all-users) SYSTEM_FLAG=true; shift ;;
    --user|--current-user) SYSTEM_FLAG=false; shift ;;
    --help|-h)
      echo "Usage: install.sh [--user|--system]"
      echo ""
      echo "  --user     Install for current user only (default, no sudo)"
      echo "  --system   Install system-wide (requires sudo)"
      exit 0 ;;
    *) shift ;;
  esac
done

# Interactive prompt (skip if piped or --system was given)
if [[ -t 0 && "${SYSTEM_FLAG}" == "false" ]]; then
  echo ""
  echo -e "${BOLD}Install ggcode for:${NC}"
  echo "  1) Current user only (~/.local/bin, no sudo required)  ${GREEN}[recommended]${NC}"
  echo "  2) System-wide (/usr/local/bin, requires sudo)"
  echo ""
  printf "Choose [1]: "
  read -r choice 2>/dev/null || choice=""
  case "${choice}" in
    2|system|System|SYSTEM) SCOPE="system" ;;
    *) SCOPE="user" ;;
  esac
fi

if [[ "${SYSTEM_FLAG}" == "true" ]]; then
  SCOPE="system"
fi

# --- Determine install directory ---
if [[ "${SCOPE}" == "system" ]]; then
  INSTALL_DIR="/usr/local/bin"
  SUDO=""
  if [[ $EUID -ne 0 ]]; then
    SUDO="sudo"
    # Check if we can sudo
    if ! ${SUDO} -n true 2>/dev/null; then
      warn "System-wide install requires sudo. You will be prompted for your password."
    fi
  fi
else
  INSTALL_DIR="${HOME}/.local/bin"
  SUDO=""
fi

info "Installing ggcode to ${BOLD}${INSTALL_DIR}${NC}"

# --- Detect platform ---
OS="$(uname -s)"
ARCH="$(uname -m)"

case "${OS}" in
  Darwin) PLATFORM="darwin" ;;
  Linux)  PLATFORM="linux" ;;
  *)
    error "Unsupported OS: ${OS}"
    exit 1
    ;;
esac

case "${ARCH}" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    error "Unsupported architecture: ${ARCH}"
    exit 1
    ;;
esac

info "Platform: ${PLATFORM}/${ARCH}"

# --- Get latest version ---
info "Fetching latest release..."
if command -v curl &>/dev/null; then
  FETCH="curl -fsSL"
elif command -v wget &>/dev/null; then
  FETCH="wget -qO-"
else
  error "Neither curl nor wget is installed."
  exit 1
fi

TAG="$(${FETCH} "${API_URL}" | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"tag_name": *"//;s/"//')"
if [[ -z "${TAG}" ]]; then
  error "Could not determine latest release version."
  exit 1
fi

VERSION="${TAG#v}"
info "Latest version: ${BOLD}${TAG}${NC}"

# --- Download ---
ARCHIVE_NAME="ggcode_${VERSION}_${PLATFORM}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${ARCHIVE_NAME}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

info "Downloading ${ARCHIVE_NAME}..."
if ! ${FETCH} -o "${TMPDIR}/${ARCHIVE_NAME}" "${DOWNLOAD_URL}" 2>/dev/null; then
  error "Download failed. Please check your internet connection."
  exit 1
fi

info "Extracting..."
tar -xzf "${TMPDIR}/${ARCHIVE_NAME}" -C "${TMPDIR}"

# Find the binary
BINARY_PATH=""
for candidate in \
  "${TMPDIR}/${BINARY_NAME}" \
  "${TMPDIR}/${BINARY_NAME}-${PLATFORM}-${ARCH}" \
  "${TMPDIR}/ggcode_${VERSION}_${PLATFORM}_${ARCH}/${BINARY_NAME}"; do
  if [[ -f "${candidate}" ]]; then
    BINARY_PATH="${candidate}"
    break
  fi
done

if [[ -z "${BINARY_PATH}" ]]; then
  # Try to find it
  BINARY_PATH="$(find "${TMPDIR}" -name "${BINARY_NAME}" -type f | head -1)"
fi

if [[ -z "${BINARY_PATH}" ]]; then
  error "Could not find ${BINARY_NAME} in archive."
  exit 1
fi

chmod +x "${BINARY_PATH}"

# --- Install ---
mkdir -p "${INSTALL_DIR}"

INSTALL_TARGET="${INSTALL_DIR}/${BINARY_NAME}"
if [[ "${SCOPE}" == "system" ]] && [[ -n "${SUDO}" ]]; then
  ${SUDO} cp "${BINARY_PATH}" "${INSTALL_TARGET}"
  ${SUDO} chmod +x "${INSTALL_TARGET}"
else
  cp "${BINARY_PATH}" "${INSTALL_TARGET}"
  chmod +x "${INSTALL_TARGET}"
fi

ok "Installed ${BINARY_NAME} ${TAG} to ${INSTALL_TARGET}"

# --- PATH setup ---
ensure_path() {
  local dir="$1"
  local shell_rc=""

  case "${SHELL:-}" in
    */zsh)  shell_rc="${HOME}/.zshrc" ;;
    */bash) shell_rc="${HOME}/.bashrc" ;;
    *)      shell_rc="${HOME}/.profile" ;;
  esac

  if [[ -z "${shell_rc}" ]]; then
    return 0
  fi

  # Check if already in PATH
  case ":${PATH}:" in
    *":${dir}:"*) return 0 ;;  # already in PATH
  esac

  echo ""
  warn "${dir} is not in your PATH."
  info "Add this line to ${shell_rc}:"
  echo -e "  ${CYAN}export PATH=\"${dir}:\$PATH\"${NC}"

  # Try to add it automatically
  if [[ -w "${shell_rc}" ]]; then
    printf "\n# Added by ggcode installer\nexport PATH=\"%s:\$PATH\"\n" "${dir}" >> "${shell_rc}"
    ok "Added to ${shell_rc}. Open a new terminal or run: source ${shell_rc}"
  fi
}

ensure_path "${INSTALL_DIR}"

# --- Verify ---
echo ""
if [[ -x "${INSTALL_TARGET}" ]]; then
  INSTALLED_VERSION="$("${INSTALL_TARGET}" version 2>/dev/null || echo "unknown")"
  ok "ggcode ${INSTALLED_VERSION} is ready."
  echo ""
  echo -e "  Run ${BOLD}${CYAN}ggcode${NC}${BOLD}${NC} to start."
  if [[ "${SCOPE}" == "system" ]]; then
    echo -e "  Update with: ${BOLD}ggcode /update${NC} or ${BOLD}sudo cp ...${NC}"
  else
    echo -e "  Update with: ${BOLD}ggcode /update${NC}"
  fi
fi
