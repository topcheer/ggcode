#!/usr/bin/env bash
# Build ggcode-desktop for Linux, plus AppImage, .deb, and .rpm release assets.
set -euo pipefail

VERSION="${1:?usage: build-desktop-linux.sh <version> <output-dir>}"
OUTPUT_DIR="${2:?usage: build-desktop-linux.sh <version> <output-dir>}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DESKTOP_DIR="${ROOT_DIR}/desktop/ggcode-desktop"
PACKAGING_DIR="${ROOT_DIR}/.github/packaging/linux"
PACKAGE_VERSION="${VERSION#v}"
COMMIT="${GGCODE_COMMIT:-}"
BUILD_DATE="${GGCODE_DATE:-}"

mkdir -p "${OUTPUT_DIR}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT
ICON_PATH="${DESKTOP_DIR}/icon.png"

detect_arch() {
  case "${TARGET_ARCH:-$(uname -m)}" in
    x86_64|amd64)
      GOARCH_VALUE="amd64"
      PACKAGE_ARCH="amd64"
      APPIMAGE_ARCH="x86_64"
      ;;
    aarch64|arm64)
      GOARCH_VALUE="arm64"
      PACKAGE_ARCH="arm64"
      APPIMAGE_ARCH="aarch64"
      ;;
    *)
      echo "unsupported target arch: ${TARGET_ARCH:-$(uname -m)}" >&2
      exit 1
      ;;
  esac
}

ensure_nfpm() {
  if command -v nfpm >/dev/null 2>&1; then
    NFPM_BIN="$(command -v nfpm)"
    return
  fi
  echo "nfpm not found in PATH; installing it into a temp tool dir"
  mkdir -p "${WORK_DIR}/bin"
  GOBIN="${WORK_DIR}/bin" go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
  NFPM_BIN="${WORK_DIR}/bin/nfpm"
}

download_appimage_tool() {
  local name="$1"
  local url="$2"
  local dest="${WORK_DIR}/tools/${name}"
  mkdir -p "${WORK_DIR}/tools"
  if [[ ! -x "${dest}" ]]; then
    curl -fsSL "${url}" -o "${dest}"
    chmod +x "${dest}"
  fi
  printf '%s\n' "${dest}"
}

build_desktop_binary() {
  local output="$1"
  pushd "${DESKTOP_DIR}" >/dev/null
  CGO_ENABLED=1 GOOS=linux GOARCH="${GOARCH_VALUE}" \
    go build -tags goolm -ldflags "${LDFLAGS[*]}" -o "${output}" .
  popd >/dev/null
  # Free disk space: clear Go build cache after compilation.
  go clean -cache 2>/dev/null || true
}

prepare_linux_icon() {
  local icon_output="${WORK_DIR}/ggcode-desktop-512.png"
  if [[ ! -f "${ICON_PATH}" ]]; then
    echo "desktop icon not found: ${ICON_PATH}" >&2
    exit 1
  fi

  convert "${ICON_PATH}" -resize 512x512 "${icon_output}"
  printf '%s\n' "${icon_output}"
}

build_linux_packages() {
  local binary="$1"
  local icon_file="$2"
  local config_path="${WORK_DIR}/ggcode-desktop.nfpm.yaml"

  cat > "${config_path}" <<EOF
name: ggcode-desktop
arch: ${PACKAGE_ARCH}
platform: linux
version: ${PACKAGE_VERSION}
section: utils
priority: optional
maintainer: GG AI Studio
vendor: GG AI Studio
homepage: https://github.com/topcheer/ggcode
description: >-
  GGCode desktop application with graphical chat, resumable sessions,
  MCP integrations, and built-in coding workflows.
license: MIT
contents:
  - src: ${binary}
    dst: /usr/bin/ggcode-desktop
    file_info:
      mode: 0755
  - src: ${PACKAGING_DIR}/ggcode-desktop.desktop
    dst: /usr/share/applications/ggcode-desktop.desktop
  - src: ${icon_file}
    dst: /usr/share/icons/hicolor/512x512/apps/ggcode-desktop.png
  - src: ${PACKAGING_DIR}/gg.ai.ggcode-desktop.metainfo.xml
    dst: /usr/share/metainfo/gg.ai.ggcode-desktop.metainfo.xml
  - src: ${ROOT_DIR}/LICENSE
    dst: /usr/share/doc/ggcode-desktop/LICENSE
  - src: ${ROOT_DIR}/README.md
    dst: /usr/share/doc/ggcode-desktop/README.md
EOF

  "${NFPM_BIN}" package \
    --packager deb \
    --config "${config_path}" \
    --target "${OUTPUT_DIR}/ggcode-desktop_${PACKAGE_VERSION}_linux_${PACKAGE_ARCH}.deb"

  "${NFPM_BIN}" package \
    --packager rpm \
    --config "${config_path}" \
    --target "${OUTPUT_DIR}/ggcode-desktop_${PACKAGE_VERSION}_linux_${PACKAGE_ARCH}.rpm"
}

build_appimage() {
  local binary="$1"
  local icon_path="$2"
  local appdir="${WORK_DIR}/AppDir"
  local linuxdeploy_bin="$3"
  local appimagetool_bin="$4"
  local desktop_entry="${PACKAGING_DIR}/ggcode-desktop.desktop"

  rm -rf "${appdir}"
  mkdir -p \
    "${appdir}/usr/bin" \
    "${appdir}/usr/share/applications" \
    "${appdir}/usr/share/icons/hicolor/512x512/apps" \
    "${appdir}/usr/share/metainfo"

  cp "${binary}" "${appdir}/usr/bin/ggcode-desktop"
  cp "${desktop_entry}" "${appdir}/usr/share/applications/ggcode-desktop.desktop"
  cp "${icon_path}" "${appdir}/usr/share/icons/hicolor/512x512/apps/ggcode-desktop.png"
  cp "${PACKAGING_DIR}/gg.ai.ggcode-desktop.metainfo.xml" \
    "${appdir}/usr/share/metainfo/gg.ai.ggcode-desktop.metainfo.xml"

  mkdir -p "${WORK_DIR}/tool-path"
  ln -sf "${appimagetool_bin}" "${WORK_DIR}/tool-path/appimagetool"

  (
    cd "${WORK_DIR}"
    PATH="${WORK_DIR}/tool-path:${PATH}" \
    APPIMAGE_EXTRACT_AND_RUN=1 \
    ARCH="${APPIMAGE_ARCH}" \
      "${linuxdeploy_bin}" \
        --appdir "${appdir}" \
        -e "${binary}" \
        -d "${desktop_entry}" \
        -i "${icon_path}" \
        --output appimage
  )

  local generated
  generated="$(find "${WORK_DIR}" -maxdepth 1 -type f -name '*.AppImage' | head -n 1)"
  if [[ -z "${generated}" ]]; then
    echo "linuxdeploy did not generate an AppImage" >&2
    exit 1
  fi

  cp "${generated}" "${OUTPUT_DIR}/ggcode-desktop_${PACKAGE_VERSION}_linux_${PACKAGE_ARCH}.AppImage"
}

detect_arch
ensure_nfpm

LINUXDEPLOY_BIN="$(download_appimage_tool "linuxdeploy" "https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-${APPIMAGE_ARCH}.AppImage")"
APPIMAGETOOL_BIN="$(download_appimage_tool "appimagetool" "https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-${APPIMAGE_ARCH}.AppImage")"

LDFLAGS=(
  -s -w
  "-X" "github.com/topcheer/ggcode/internal/version.Version=${VERSION}"
  "-X" "github.com/topcheer/ggcode/internal/version.Commit=${COMMIT}"
  "-X" "github.com/topcheer/ggcode/internal/version.Date=${BUILD_DATE}"
  "-X" "main.Version=${VERSION}"
)

echo "=== Building ggcode-desktop for Linux (${PACKAGE_ARCH}) ==="

RAW_BINARY="${WORK_DIR}/ggcode-desktop"
PACKAGED_ICON="$(prepare_linux_icon)"
build_desktop_binary "${RAW_BINARY}"
chmod 0755 "${RAW_BINARY}"

cp "${RAW_BINARY}" "${OUTPUT_DIR}/ggcode-desktop_${PACKAGE_VERSION}_linux_${PACKAGE_ARCH}"
build_linux_packages "${RAW_BINARY}" "${PACKAGED_ICON}"
build_appimage "${RAW_BINARY}" "${PACKAGED_ICON}" "${LINUXDEPLOY_BIN}" "${APPIMAGETOOL_BIN}"

echo "=== Done: ${OUTPUT_DIR}/ggcode-desktop_${PACKAGE_VERSION}_linux_${PACKAGE_ARCH} (+ AppImage/.deb/.rpm) ==="
