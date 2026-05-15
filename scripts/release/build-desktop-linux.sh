#!/usr/bin/env bash
# Build ggcode-desktop for Linux (amd64)
set -euo pipefail

VERSION="${1:?usage: build-desktop-linux.sh <version> <output-dir>}"
OUTPUT_DIR="${2:?usage: build-desktop-linux.sh <version> <output-dir>}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DESKTOP_DIR="${ROOT_DIR}/desktop/ggcode-desktop"
PACKAGE_VERSION="${VERSION#v}"
COMMIT="${GGCODE_COMMIT:-}"
BUILD_DATE="${GGCODE_DATE:-}"

mkdir -p "${OUTPUT_DIR}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

LDFLAGS=(
  -s -w
  "-X" "github.com/topcheer/ggcode/internal/version.Version=${VERSION}"
  "-X" "github.com/topcheer/ggcode/internal/version.Commit=${COMMIT}"
  "-X" "github.com/topcheer/ggcode/internal/version.Date=${BUILD_DATE}"
)

echo "=== Building ggcode-desktop for Linux (amd64) ==="

pushd "${DESKTOP_DIR}" >/dev/null
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -tags goolm -ldflags "${LDFLAGS[*]}" -o "${WORK_DIR}/ggcode-desktop" .
popd >/dev/null

chmod 0755 "${WORK_DIR}/ggcode-desktop"
cp "${WORK_DIR}/ggcode-desktop" "${OUTPUT_DIR}/ggcode-desktop_${PACKAGE_VERSION}_linux_amd64"

echo "=== Done: ${OUTPUT_DIR}/ggcode-desktop_${PACKAGE_VERSION}_linux_amd64 ==="
