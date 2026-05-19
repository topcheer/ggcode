#!/bin/bash
# deploy_macos.sh — Build, sign, notarize, and package GGCode Desktop for macOS
#
# Usage:
#   ./scripts/deploy_macos.sh          # Full: build + sign + notarize + staple
#   ./scripts/deploy_macos.sh build    # Build + sign only (no notarize)
#   ./scripts/deploy_macos.sh notarize # Notarize existing zip only
#
set -euo pipefail

cd "$(dirname "$0")/.."
cd desktop/ggcode-desktop

VERSION="${1:-$(grep -oP 'v\d+\.\d+\.\d+' <<< "$(git describe --tags --abbrev=0 2>/dev/null || echo v1.3.7)")}"
# Strip 'v' prefix for Apple
VERSION_NUM="${VERSION#v}"
ZIP_NAME="GGCode-macOS-${VERSION}.zip"
TEAM_ID="EGZFS7M525"
SIGN_IDENTITY="Developer ID Application: Junjun Zhang (${TEAM_ID})"
KEY_FILE="../../mobile/flutter/secrets/AuthKey_8KNYUZ47F2.p8"
KEY_ID="8KNYUZ47F2"
ISSUER_ID="69a6de7d-3567-47e3-e053-5b8c7c11a4d1"

echo "=== GGCode Desktop v${VERSION_NUM} ==="

# Step 1: Build
echo "[1/5] Building..."
go build -tags goolm -ldflags "-X main.Version=${VERSION}" -o ggcode.app/Contents/MacOS/desktop .

# Step 2: Update Info.plist version
echo "[2/5] Updating Info.plist..."
sed -i '' "s|<string>[0-9]\+\.[0-9]\+\.[0-9]\+</string><!-- VERSION -->|"\
"<string>${VERSION_NUM}</string><!-- VERSION -->|" ggcode.app/Contents/Info.plist 2>/dev/null || true
# Direct sed for CFBundleShortVersionString
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString ${VERSION_NUM}" ggcode.app/Contents/Info.plist

# Step 3: Codesign
echo "[3/5] Codesigning..."
codesign --force --deep --options runtime \
  --entitlements entitlements.plist \
  --sign "${SIGN_IDENTITY}" ggcode.app
codesign -vvv --deep --strict ggcode.app

# Step 4: Zip and Notarize
echo "[4/5] Packaging..."
rm -f GGCode-macOS-*.zip
ditto -c -k --keepParent ggcode.app "${ZIP_NAME}"

echo "[5/5] Notarizing (this takes a few minutes)..."
xcrun notarytool submit "${ZIP_NAME}" \
  --key "${KEY_FILE}" \
  --key-id "${KEY_ID}" \
  --issuer "${ISSUER_ID}" \
  --wait

# Staple the ticket
echo "Stapling notarization ticket..."
xcrun stapler staple ggcode.app

# Re-zip with stapled ticket
rm -f "${ZIP_NAME}"
ditto -c -k --keepParent ggcode.app "${ZIP_NAME}"

echo ""
echo "=== Done: ${ZIP_NAME} ($(du -h "${ZIP_NAME}" | cut -f1)) ==="
echo "Upload to GitHub Releases or distribute directly."
