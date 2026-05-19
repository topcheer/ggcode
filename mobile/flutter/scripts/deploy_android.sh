#!/bin/bash
# deploy_android.sh — Build and deploy GGCode Mobile to Google Play
# Usage:
#   ./scripts/deploy_android.sh internal    # Upload to Internal Testing
#   ./scripts/deploy_android.sh alpha       # Promote internal -> alpha
#   ./scripts/deploy_android.sh production  # Promote alpha -> production
set -euo pipefail

cd "$(dirname "$0")/.."

# Ensure fastlane is installed
if ! command -v bundle &>/dev/null; then
  echo "Installing bundler..."
  gem install bundler
fi

cd android
bundle install --quiet 2>/dev/null || true

LANE="${1:-deploy_internal}"
echo "=== Android Deploy: lane=$LANE ==="

case "$LANE" in
  build)
    bundle exec fastlane build_apk
    ;;
  aab)
    bundle exec fastlane build_aab
    ;;
  internal)
    bundle exec fastlane deploy_internal
    ;;
  alpha)
    bundle exec fastlane promote_alpha
    ;;
  production)
    bundle exec fastlane promote_production
    ;;
  *)
    echo "Unknown lane: $LANE"
    echo "Usage: $0 [build|aab|internal|alpha|production]"
    exit 1
    ;;
esac

echo "=== Done ==="
