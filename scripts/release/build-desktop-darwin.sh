#!/usr/bin/env bash
# Build ggcode-desktop for macOS: universal binary + .app bundle + codesign + notarize + .dmg
set -euo pipefail

VERSION="${1:?usage: build-desktop-darwin.sh <version> <output-dir>}"
OUTPUT_DIR="${2:?usage: build-desktop-darwin.sh <version> <output-dir>}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DESKTOP_DIR="${ROOT_DIR}/desktop/ggcode-desktop"
PACKAGE_VERSION="${VERSION#v}"
COMMIT="${GGCODE_COMMIT:-}"
BUILD_DATE="${GGCODE_DATE:-}"
APP_ID="gg.ai.ggcode"
APP_NAME="ggcode-desktop"
EXEC_NAME="desktop"

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

LDFLAGS=(
  -s -w
  "-X" "github.com/topcheer/ggcode/internal/version.Version=${VERSION}"
  "-X" "github.com/topcheer/ggcode/internal/version.Commit=${COMMIT}"
  "-X" "github.com/topcheer/ggcode/internal/version.Date=${BUILD_DATE}"
  "-X" "main.Version=${VERSION}"
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

# Free disk space: remove per-architecture binaries and Go build cache.
rm -f "${WORK_DIR}/${APP_NAME}-amd64" "${WORK_DIR}/${APP_NAME}-arm64"
go clean -cache 2>/dev/null || true

echo "  Creating .app bundle..."
APP_BUNDLE="${WORK_DIR}/${APP_NAME}.app"
mkdir -p "${APP_BUNDLE}/Contents/MacOS"
mkdir -p "${APP_BUNDLE}/Contents/Resources"
cp "${WORK_DIR}/${APP_NAME}" "${APP_BUNDLE}/Contents/MacOS/${EXEC_NAME}"

# Copy icon if available
if [[ -f "${DESKTOP_DIR}/ggcode.app/Contents/Resources/icon.icns" ]]; then
  cp "${DESKTOP_DIR}/ggcode.app/Contents/Resources/icon.icns" "${APP_BUNDLE}/Contents/Resources/icon.icns"
fi

# Info.plist
cat > "${APP_BUNDLE}/Contents/Info.plist" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key>
  <string>${EXEC_NAME}</string>
  <key>CFBundleIdentifier</key>
  <string>${APP_ID}</string>
  <key>CFBundleName</key>
  <string>GGCode</string>
  <key>CFBundleDisplayName</key>
  <string>GGCode</string>
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
  <key>LSApplicationCategoryType</key>
  <string>public.app-category.developer-tools</string>
  <key>NSHighResolutionCapable</key>
  <true/>
  <key>NSSupportsAutomaticGraphicsSwitching</key>
  <true/>
  <key>ITSAppUsesNonExemptEncryption</key>
  <false/>
  <key>NSHumanReadableCopyright</key>
  <string>Copyright 2025 GG AI. MIT License.</string>
</dict>
</plist>
PLIST

# ── Codesign ──────────────────────────────────────────────
if [[ "${DO_SIGN}" == "true" ]]; then
  # Import certificate and configure keychain for codesigning.
  echo "  DO_SIGN=true, P12_PATH=${P12_PATH}, SIGN_IDENTITY=${SIGN_IDENTITY}"
  set -x
  KEYCHAIN="${WORK_DIR}/signing.keychain-db"
  KEYCHAIN_PASSWORD="ci-$(date +%s)"
  security create-keychain -p "${KEYCHAIN_PASSWORD}" "${KEYCHAIN}"
  security set-keychain-settings -lut 21600 "${KEYCHAIN}"
  security unlock-keychain -p "${KEYCHAIN_PASSWORD}" "${KEYCHAIN}"
  security import "${P12_PATH}" -P "${P12_PASSWORD}" -A -t cert -f pkcs12 -k "${KEYCHAIN}"
  echo "  Import exit code: $?"
  file "${P12_PATH}"
  # Verify identity is available after import
  security find-identity -v -p codesigning "${KEYCHAIN}" || echo "  WARNING: No signing identities found after import"
  # set-key-partition-list may fail on some runners; allow codesign access via -T.
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "${KEYCHAIN_PASSWORD}" "${KEYCHAIN}" 2>/dev/null || true
  # Add custom keychain to the search list so codesign can find the identity.
  security list-keychains -d user -s "${KEYCHAIN}" login.keychain-db 2>/dev/null || true

  echo "  Available signing identities:"
  security find-identity -v -p codesigning "${KEYCHAIN}" 2>/dev/null || true

  echo "  Codesigning..."
  codesign --force --deep --options runtime \
    --entitlements "${DESKTOP_DIR}/entitlements.plist" \
    --sign "${SIGN_IDENTITY}" \
    --keychain "${KEYCHAIN}" \
    "${APP_BUNDLE}"
  codesign -vvv --deep --strict "${APP_BUNDLE}"
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
cp -R "${APP_BUNDLE}" "${DMG_STAGING}/"
ln -s /Applications "${DMG_STAGING}/Applications"

# Free disk space: remove .app bundle from WORK_DIR (now copied to staging).
rm -rf "${APP_BUNDLE}"

hdiutil create -volname "GGCode Desktop" -srcfolder "${DMG_STAGING}" -ov -format UDZO "${DMG_PATH}"

# Free disk space: remove DMG staging after hdiutil.
rm -rf "${DMG_STAGING}"

# ── Notarize ───────────────────────────────────────────────
if [[ "${DO_NOTARIZE}" == "true" ]]; then
  echo "  Notarizing (this takes a few minutes)..."
  xcrun notarytool submit "${DMG_PATH}" \
    --key "${API_KEY_PATH}" \
    --key-id "${API_KEY_ID}" \
    --issuer "${API_ISSUER_ID}" \
    --wait

  echo "  Stapling notarization ticket..."
  xcrun stapler staple "${APP_BUNDLE}"

  # Re-create DMG with stapled ticket
  rm -f "${DMG_PATH}"
  mkdir -p "${DMG_STAGING}"
  cp -R "${APP_BUNDLE}" "${DMG_STAGING}/"
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
