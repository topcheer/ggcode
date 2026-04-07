#!/usr/bin/env bash

set -euo pipefail

VERSION="${1:?usage: smoke-installer.sh <version> <dist-dir>}"
DIST_DIR="${2:?usage: smoke-installer.sh <version> <dist-dir>}"
INSTALL_GOOS="${GGCODE_INSTALL_GOOS:-linux}"
INSTALL_GOARCH="${GGCODE_INSTALL_GOARCH:-amd64}"

case "${INSTALL_GOARCH}" in
  amd64) ARCH_SUFFIX="x86_64" ;;
  arm64) ARCH_SUFFIX="arm64" ;;
  *)
    echo "unsupported architecture: ${INSTALL_GOARCH}" >&2
    exit 1
    ;;
esac

case "${INSTALL_GOOS}" in
  linux|darwin)
    ARCHIVE_NAME="ggcode_${INSTALL_GOOS}_${ARCH_SUFFIX}.tar.gz"
    INSTALLED_BINARY="ggcode"
    ;;
  windows)
    ARCHIVE_NAME="ggcode_windows_${ARCH_SUFFIX}.zip"
    INSTALLED_BINARY="ggcode.exe"
    ;;
  *)
    echo "unsupported OS: ${INSTALL_GOOS}" >&2
    exit 1
    ;;
esac

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK_DIR="$(mktemp -d)"
SERVER_PID=""
trap 'if [[ -n "${SERVER_PID}" ]]; then kill "${SERVER_PID}" >/dev/null 2>&1 || true; fi; rm -rf "${WORK_DIR}"' EXIT

RELEASE_DIR="${WORK_DIR}/releases/download/${VERSION}"
INSTALL_DIR="${WORK_DIR}/bin"
mkdir -p "${RELEASE_DIR}" "${INSTALL_DIR}"

cp "${DIST_DIR}/checksums.txt" "${RELEASE_DIR}/"
cp "${DIST_DIR}/${ARCHIVE_NAME}" "${RELEASE_DIR}/"

PORT="$(
  python3 - <<'PY'
import socket
sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
)"

python3 -m http.server "${PORT}" --bind 127.0.0.1 --directory "${WORK_DIR}" >/dev/null 2>&1 &
SERVER_PID=$!
sleep 2

INSTALLER_BIN="${WORK_DIR}/ggcode-installer"
(
  cd "${ROOT_DIR}"
  go build -o "${INSTALLER_BIN}" ./cmd/ggcode-installer
)

GGCODE_INSTALL_BASE_URL="http://127.0.0.1:${PORT}" \
  GGCODE_INSTALL_GOOS="${INSTALL_GOOS}" \
  GGCODE_INSTALL_GOARCH="${INSTALL_GOARCH}" \
"${INSTALLER_BIN}" -version "${VERSION}" -dir "${INSTALL_DIR}"

if [[ "${INSTALL_GOOS}" == "windows" ]]; then
  pwsh -NoProfile -File "${ROOT_DIR}/scripts/release/smoke-binary.ps1" -BinaryPath "${INSTALL_DIR}/${INSTALLED_BINARY}"
else
  "${ROOT_DIR}/scripts/release/smoke-binary.sh" "${INSTALL_DIR}/${INSTALLED_BINARY}"
fi
