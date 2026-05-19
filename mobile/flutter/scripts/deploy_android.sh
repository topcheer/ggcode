#!/bin/bash
# deploy_android.sh — Build and deploy GGCode Mobile to Google Play
#
# Usage:
#   ./scripts/deploy_android.sh            # Build → Internal → Closed Testing
#   ./scripts/deploy_android.sh internal   # Build → Internal Testing only
#   ./scripts/deploy_android.sh alpha      # Promote internal → Closed Testing
#   ./scripts/deploy_android.sh production # Promote alpha → production
#   ./scripts/deploy_android.sh metadata   # Upload metadata + screenshots only
#
set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v bundle &>/dev/null; then
  echo "Installing bundler..."
  gem install bundler
fi

cd android
bundle install --quiet 2>/dev/null || true

LANE="${1:-deploy}"
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
  metadata)
    bundle exec fastlane upload_metadata
    ;;
  deploy)
    bundle exec fastlane deploy
    ;;
  *)
    echo "Unknown lane: $LANE"
    echo "Usage: $0 [build|aab|internal|alpha|production|metadata|deploy]"
    exit 1
    ;;
esac

echo "=== Done ==="
