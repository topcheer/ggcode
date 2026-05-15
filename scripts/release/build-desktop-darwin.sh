#!/usr/bin/env bash
# Build ggcode-desktop for macOS: universal binary + .app bundle + .dmg
set -euo pipefail

VERSION="${1:?usage: build-desktop-darwin.sh <version> <output-dir>}"
OUTPUT_DIR="${2:?usage: build-desktop-darwin.sh <version> <output-dir>}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DESKTOP_DIR="${ROOT_DIR}/desktop/ggcode-desktop"
PACKAGE_VERSION="${VERSION#v}"
COMMIT="${GGCODE_COMMIT:-}"
BUILD_DATE="${GGCODE_DATE:-}"
APP_ID="com.ggcode.desktop"
APP_NAME="ggcode-desktop"

mkdir -p "${OUTPUT_DIR}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

LDFLAGS=(
  -s -w
  "-X" "github.com/topcheer/ggcode/internal/version.Version=${VERSION}"
  "-X" "github.com/topcheer/ggcode/internal/version.Commit=${COMMIT}"
  "-X" "github.com/topcheer/ggcode/internal/version.Date=${BUILD_DATE}"
)

echo "=== Building ggcode-desktop for macOS (universal) ==="

pushd "${DESKTOP_DIR}" >/dev/null
for arch in amd64 arm64; do
  echo "  Building for ${arch}..."
  CGO_ENABLED=1 GOOS=darwin GOARCH="${arch}" \
    go build -tags goolm -ldflags "${LDFLAGS[*]}" -o "${WORK_DIR}/${APP_NAME}-${arch}" .
done
popd >/dev/null

echo "  Creating universal binary..."
lipo -create -output "${WORK_DIR}/${APP_NAME}" "${WORK_DIR}/${APP_NAME}-amd64" "${WORK_DIR}/${APP_NAME}-arm64"
chmod 0755 "${WORK_DIR}/${APP_NAME}"

echo "  Creating .app bundle..."
APP_BUNDLE="${WORK_DIR}/${APP_NAME}.app"
mkdir -p "${APP_BUNDLE}/Contents/MacOS"
mkdir -p "${APP_BUNDLE}/Contents/Resources"
cp "${WORK_DIR}/${APP_NAME}" "${APP_BUNDLE}/Contents/MacOS/${APP_NAME}"

# Info.plist
cat > "${APP_BUNDLE}/Contents/Info.plist" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key>
  <string>${APP_NAME}</string>
  <key>CFBundleIdentifier</key>
  <string>${APP_ID}</string>
  <key>CFBundleName</key>
  <string>ggcode Desktop</string>
  <key>CFBundleDisplayName</key>
  <string>ggcode</string>
  <key>CFBundleVersion</key>
  <string>${PACKAGE_VERSION}</string>
  <key>CFBundleShortVersionString</key>
  <string>${PACKAGE_VERSION}</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleIconFile</key>
  <string>icon.icns</string>
  <key>LSMinimumSystemVersion</key>
  <string>11.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
  <key>NSHumanReadableCopyright</key>
  <string>Copyright 2025 topcheer. MIT License.</string>
</dict>
</plist>
PLIST

# Create DMG
echo "  Creating .dmg..."
DMG_NAME="ggcode-desktop_${PACKAGE_VERSION}_darwin_universal.dmg"
DMG_PATH="${OUTPUT_DIR}/${DMG_NAME}"
DMG_STAGING="${WORK_DIR}/dmg-staging"
mkdir -p "${DMG_STAGING}"
cp -R "${APP_BUNDLE}" "${DMG_STAGING}/"
ln -s /Applications "${DMG_STAGING}/Applications"

hdiutil create -volname "ggcode Desktop" -srcfolder "${DMG_STAGING}" -ov -format UDZO "${DMG_PATH}"

echo "=== Done: ${DMG_PATH} ==="
