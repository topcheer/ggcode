#!/bin/bash
# deploy_ios.sh — Build and deploy GGCode Mobile to iOS App Store
# Usage:
#   ./scripts/deploy_ios.sh testflight   # Upload to TestFlight
#   ./scripts/deploy_ios.sh release      # Submit for App Store Review
#
# Prerequisites:
#   - Copy .env.example to .env and fill in APP_STORE_KEY_ID and APP_STORE_ISSUER_ID
#   - Ensure secrets/AuthKey.p8 exists (symlink to real key)
set -euo pipefail

cd "$(dirname "$0")/.."

# Load .env if exists
if [ -f .env ]; then
  set -a
  source .env
  set +a
fi

# Validate required env vars for non-build lanes
LANE="${1:-upload_testflight}"
if [ "$LANE" != "build" ]; then
  if [ -z "${APP_STORE_KEY_ID:-}" ] || [ -z "${APP_STORE_ISSUER_ID:-}" ]; then
    echo "ERROR: APP_STORE_KEY_ID and APP_STORE_ISSUER_ID must be set in .env"
    echo "  cp .env.example .env  # then fill in values"
    exit 1
  fi
fi

# Ensure fastlane is installed
if ! command -v bundle &>/dev/null; then
  echo "Installing bundler..."
  gem install bundler
fi

cd ios
bundle install --quiet 2>/dev/null || true

echo "=== iOS Deploy: lane=$LANE ==="

case "$LANE" in
  build)
    bundle exec fastlane build
    ;;
  testflight)
    bundle exec fastlane upload_testflight
    ;;
  release)
    bundle exec fastlane release
    ;;
  *)
    echo "Unknown lane: $LANE"
    echo "Usage: $0 [build|testflight|release]"
    exit 1
    ;;
esac

echo "=== Done ==="
