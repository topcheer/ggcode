#!/bin/bash
# deploy_ios.sh — Build and deploy GGCode Mobile to iOS App Store
#
# Usage:
#   ./scripts/deploy_ios.sh metadata   # Upload metadata only (no build)
#   ./scripts/deploy_ios.sh build      # Build IPA only (flutter build ipa)
#   ./scripts/deploy_ios.sh testflight # Build + upload to TestFlight
#   ./scripts/deploy_ios.sh release    # Build + submit for App Store Review
#
set -euo pipefail

cd "$(dirname "$0")/.."

# Load .env if exists
if [ -f .env ]; then
  set -a
  source <(grep -v '^#' .env)
  set +a
fi

LANE="${1:-testflight}"

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

case "$LANE" in
  metadata)
    cd ios
    bundle install --quiet 2>/dev/null || true
    bundle exec fastlane upload_metadata
    ;;
  build)
    echo "--- flutter build ipa ---"
    flutter build ipa --release
    echo "IPA: build/ios/ipa/ggcode_mobile.ipa"
    ;;
  testflight)
    echo "--- Step 1: flutter build ipa ---"
    flutter build ipa --release
    echo "--- Step 2: upload to TestFlight ---"
    cd ios
    bundle install --quiet 2>/dev/null || true
    bundle exec fastlane upload_testflight
    ;;
  release)
    echo "--- Step 1: flutter build ipa ---"
    flutter build ipa --release
    echo "--- Step 2: submit for review ---"
    cd ios
    bundle install --quiet 2>/dev/null || true
    bundle exec fastlane release
    ;;
  *)
    echo "Unknown lane: $LANE"
    echo "Usage: $0 [metadata|build|testflight|release]"
    exit 1
    ;;
esac

echo "=== Done ==="
