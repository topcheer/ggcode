#!/usr/bin/env bash
# Build GGCode Desktop (Wails) for macOS: universal binary + .app bundle + codesign + notarize + .dmg
set -euo pipefail

VERSION="${1:?usage: build-desktop-darwin.sh <version> <output-dir>}"
OUTPUT_DIR="${2:?usage: build-desktop-darwin.sh <version> <output-dir>}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WAILS_DIR="${ROOT_DIR}/desktop/ggcode-desktop-wails"
PACKAGE_VERSION="${VERSION#v}"
COMMIT="${GGCODE_COMMIT:-}"
BUILD_DATE="${GGCODE_DATE:-}"
APP_NAME="GGCode-Desktop"
APP_ID="com.ggcode.desktop"

# Signing / notarization (set by CI via environment or skip if not available)
SIGN_IDENTITY="${APPLE_SIGN_IDENTITY:-}"
P12_PATH="${APPLE_DEVELOPER_ID_P12_PATH:-}"
P12_PASSWORD="${APPLE_DEVELOPER_ID_P12_PASSWORD:-}"
API_KEY_PATH="${APPLE_API_KEY_PATH:-}"
API_KEY_ID="${APPLE_API_KEY_ID:-}"
API_ISSUER_ID="${APPLE_API_ISSUER_ID:-}"

DO_SIGN=false
DO_NOTARIZE=false
if [[ -n "${SIGN_IDENTITY}" && -n "${P12_PATH}" ]]; then
  DO_SIGN=true
  if [[ -n "${API_KEY_PATH}" && -n "${API_KEY_ID}" && -n "${API_ISSUER_ID}" ]]; then
    DO_NOTARIZE=true
  fi
fi

mkdir -p "${OUTPUT_DIR}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

# Install Wails CLI if not present
ensure_wails() {
  if command -v wails >/dev/null 2>&1; then
    return
  fi
  echo "Installing Wails CLI..."
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
}

# Update wails.json product version
update_wails_version() {
  if [[ "$(uname)" == "Darwin" ]]; then
    sed -i '' "s/\"productVersion\": \".*\"/\"productVersion\": \"${PACKAGE_VERSION}\"/" "${WAILS_DIR}/wails.json"
  else
    sed -i "s/\"productVersion\": \".*\"/\"productVersion\": \"${PACKAGE_VERSION}\"/" "${WAILS_DIR}/wails.json"
  fi
}

echo "=== Building GGCode Desktop (Wails) for macOS (universal) ==="

ensure_wails
update_wails_version

# Build with Wails (produces .app bundle in build/bin/)
pushd "${WAILS_DIR}" >/dev/null

LDFLAGS="-s -w -X github.com/topcheer/ggcode/internal/version.Version=${VERSION} -X github.com/topcheer/ggcode/internal/version.Commit=${COMMIT} -X github.com/topcheer/ggcode/internal/version.Date=${BUILD_DATE}"

echo "  Building for amd64..."
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
  wails build -tags goolm -ldflags "${LDFLAGS}" -platform darwin/amd64 -clean -skipbindings
# Wails uses wails.json "name" for the .app bundle name, not -o flag.
# Rename to arch-specific name before building the next arch.
mv "${WAILS_DIR}/build/bin/GGCode Desktop.app" "${WAILS_DIR}/build/bin/${APP_NAME}-amd64.app" 2>/dev/null || true

echo "  Building for arm64..."
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  wails build -tags goolm -ldflags "${LDFLAGS}" -platform darwin/arm64 -skipbindings
mv "${WAILS_DIR}/build/bin/GGCode Desktop.app" "${WAILS_DIR}/build/bin/${APP_NAME}-arm64.app" 2>/dev/null || true

popd >/dev/null

# Create universal binary from the two .app bundles
AMD64_APP="${WAILS_DIR}/build/bin/${APP_NAME}-amd64.app"
ARM64_APP="${WAILS_DIR}/build/bin/${APP_NAME}-arm64.app"
UNIVERSAL_APP="${WORK_DIR}/${APP_NAME}.app"

echo "  Creating universal .app bundle..."
cp -R "${AMD64_APP}" "${UNIVERSAL_APP}"

# Merge the executables into a universal binary
EXEC_PATH="Contents/MacOS/${APP_NAME}"
lipo -create \
  "${AMD64_APP}/${EXEC_PATH}" \
  "${ARM64_APP}/${EXEC_PATH}" \
  -output "${UNIVERSAL_APP}/${EXEC_PATH}"
chmod 0755 "${UNIVERSAL_APP}/${EXEC_PATH}"

# Free disk space
rm -rf "${WAILS_DIR}/build/bin"
go clean -cache 2>/dev/null || true

# ── Codesign ──────────────────────────────────────────────
if [[ "${DO_SIGN}" == "true" ]]; then
  echo "  Codesigning..."
  KEYCHAIN="${WORK_DIR}/signing.keychain-db"
  KEYCHAIN_PASSWORD="ci-$(date +%s)"
  security create-keychain -p "${KEYCHAIN_PASSWORD}" "${KEYCHAIN}"
  security set-keychain-settings -lut 21600 "${KEYCHAIN}"
  security unlock-keychain -p "${KEYCHAIN_PASSWORD}" "${KEYCHAIN}"
  security import "${P12_PATH}" -P "${P12_PASSWORD}" -A -t cert -f pkcs12 -k "${KEYCHAIN}"
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "${KEYCHAIN_PASSWORD}" "${KEYCHAIN}" 2>/dev/null || true
  security list-keychains -d user -s "${KEYCHAIN}" login.keychain-db 2>/dev/null || true

  codesign --force --deep --options runtime \
    --sign "${SIGN_IDENTITY}" \
    --keychain "${KEYCHAIN}" \
    "${UNIVERSAL_APP}"
  codesign -vvv --deep --strict "${UNIVERSAL_APP}"
  echo "  Codesign OK"
else
  echo "  Skipping codesign (no signing identity)"
fi

# ── Create DMG ─────────────────────────────────────────────
echo "  Creating .dmg..."
DMG_NAME="ggcode-desktop_${PACKAGE_VERSION}_darwin_universal.dmg"
DMG_PATH="${OUTPUT_DIR}/${DMG_NAME}"
DMG_STAGING="${WORK_DIR}/dmg-staging"
mkdir -p "${DMG_STAGING}"
cp -R "${UNIVERSAL_APP}" "${DMG_STAGING}/"
ln -s /Applications "${DMG_STAGING}/Applications"

hdiutil create -volname "GGCode Desktop" -srcfolder "${DMG_STAGING}" -ov -format UDZO "${DMG_PATH}"
rm -rf "${DMG_STAGING}"

# ── Notarize ───────────────────────────────────────────────
if [[ "${DO_NOTARIZE}" == "true" ]]; then
  echo "  Notarizing..."
  xcrun notarytool submit "${DMG_PATH}" \
    --key "${API_KEY_PATH}" \
    --key-id "${API_KEY_ID}" \
    --issuer "${API_ISSUER_ID}" \
    --wait

  echo "  Stapling notarization ticket..."
  xcrun stapler staple "${UNIVERSAL_APP}"

  # Re-create DMG with stapled ticket
  rm -f "${DMG_PATH}"
  mkdir -p "${DMG_STAGING}"
  cp -R "${UNIVERSAL_APP}" "${DMG_STAGING}/"
  ln -s /Applications "${DMG_STAGING}/Applications"
  hdiutil create -volname "GGCode Desktop" -srcfolder "${DMG_STAGING}" -ov -format UDZO "${DMG_PATH}"
  rm -rf "${DMG_STAGING}"
  echo "  Notarization + Staple OK"
else
  echo "  Skipping notarization (no API key)"
fi

# Cleanup keychain
if [[ "${DO_SIGN}" == "true" ]]; then
  security delete-keychain "${KEYCHAIN}" 2>/dev/null || true
fi

echo "=== Done: ${DMG_PATH} ==="
