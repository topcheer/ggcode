#!/bin/bash
# deploy_ios.sh — Build and deploy GGCode Mobile to iOS App Store
#
# Usage:
#   ./scripts/deploy_ios.sh metadata   # Upload metadata only (no build)
#   ./scripts/deploy_ios.sh build      # Build archive only
#   ./scripts/deploy_ios.sh testflight # Build + upload to TestFlight
#   ./scripts/deploy_ios.sh release    # Build + submit for App Store Review
#
set -euo pipefail

cd "$(dirname "$0")/.."

# Load .env if exists
if [ -f .env ]; then
  set -a
  source .env
  set +a
fi

LANE="${1:-upload_testflight}"

# Validate env vars for non-build lanes
if [ "$LANE" != "build" ] && [ "$LANE" != "metadata" ]; then
  if [ -z "${APP_STORE_KEY_ID:-}" ] || [ -z "${APP_STORE_ISSUER_ID:-}" ]; then
    echo "ERROR: APP_STORE_KEY_ID and APP_STORE_ISSUER_ID must be set in .env"
    echo "  cp .env.example .env  # then fill in values"
    exit 1
  fi
fi

if ! command -v bundle &>/dev/null; then
  echo "Installing bundler..."
  gem install bundler
fi

echo "=== iOS Deploy: lane=$LANE ==="

# For build-related lanes, first run flutter build to update Generated.xcconfig
# (FLUTTER_BUILD_NAME and FLUTTER_BUILD_NUMBER) from pubspec.yaml
if [ "$LANE" != "metadata" ]; then
  echo "--- flutter build ipa (updating version info) ---"
  flutter build ipa --release 2>&1 || {
    echo "WARNING: flutter build ipa failed (may be expected if archive-only mode)"
    echo "Will proceed with fastlane gym for archive + upload"
  }
fi

cd ios
bundle install --quiet 2>/dev/null || true

case "$LANE" in
  metadata)
    bundle exec fastlane upload_metadata
    ;;
  build)
    bundle exec fastlane build
    ;;
  testflight)
    # Use the ipa built by flutter if available, otherwise fall back to gym
    IPA_PATH="../build/ios/ipa/ggcode_mobile.ipa"
    if [ -f "$IPA_PATH" ]; then
      echo "--- Uploading flutter-built IPA to TestFlight ---"
      bundle exec fastlane upload_to_testflight_only ipa:"../../build/ios/ipa/ggcode_mobile.ipa"
    else
      echo "--- No flutter IPA found, using gym ---"
      bundle exec fastlane upload_testflight
    fi
    ;;
  release)
    bundle exec fastlane release
    ;;
  *)
    echo "Unknown lane: $LANE"
    echo "Usage: $0 [metadata|build|testflight|release]"
    exit 1
    ;;
esac

echo "=== Done ==="
