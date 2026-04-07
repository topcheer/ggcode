#!/usr/bin/env bash

set -euo pipefail

VERSION="${1:?usage: build-macos-pkg.sh <version> <output-dir>}"
OUTPUT_DIR="${2:?usage: build-macos-pkg.sh <version> <output-dir>}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PACKAGE_VERSION="${VERSION#v}"
COMMIT="${GGCODE_COMMIT:-}"
BUILD_DATE="${GGCODE_DATE:-}"

mkdir -p "${OUTPUT_DIR}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

LDFLAGS=(
  -s
  -w
  "-X" "github.com/topcheer/ggcode/internal/version.Version=${VERSION}"
  "-X" "github.com/topcheer/ggcode/internal/version.Commit=${COMMIT}"
  "-X" "github.com/topcheer/ggcode/internal/version.Date=${BUILD_DATE}"
)

pushd "${ROOT_DIR}" >/dev/null
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=darwin GOARCH="${arch}" \
    go build -ldflags "${LDFLAGS[*]}" -o "${WORK_DIR}/ggcode-${arch}" ./cmd/ggcode
done
popd >/dev/null

lipo -create -output "${WORK_DIR}/ggcode" "${WORK_DIR}/ggcode-amd64" "${WORK_DIR}/ggcode-arm64"
chmod 0755 "${WORK_DIR}/ggcode"

PKG_ROOT="${WORK_DIR}/root"
mkdir -p "${PKG_ROOT}/usr/local/bin"
cp "${WORK_DIR}/ggcode" "${PKG_ROOT}/usr/local/bin/ggcode"

pkgbuild \
  --root "${PKG_ROOT}" \
  --identifier "cc.topcheer.ggcode" \
  --version "${PACKAGE_VERSION}" \
  --install-location "/" \
  "${OUTPUT_DIR}/ggcode_${PACKAGE_VERSION}_darwin_universal.pkg"
