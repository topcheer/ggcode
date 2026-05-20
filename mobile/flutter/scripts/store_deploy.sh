#!/bin/bash
# store_deploy.sh — One-command mobile app store deployment
#
# Usage:
#   ./scripts/store_deploy.sh 1.3.9                  # Deploy both platforms
#   ./scripts/store_deploy.sh 1.3.9 ios              # iOS only
#   ./scripts/store_deploy.sh 1.3.9 android          # Android only
#   ./scripts/store_deploy.sh 1.3.9 --skip-review    # Upload only, don't submit for review
#   ./scripts/store_deploy.sh --current              # Show current version info
#   ./scripts/store_deploy.sh --screenshots          # Upload metadata + screenshots only
#
# Flow:
#   1. Sync version to pubspec.yaml, build.gradle.kts, Info.plist
#   2. Build iOS IPA (flutter build ipa)
#   3. Upload iOS to TestFlight
#   4. Build Android AAB (flutter build appbundle)
#   5. Upload Android to Internal → promote to Alpha
#   6. (Optional) Submit for review
#   7. Commit version bump to git
#
set -euo pipefail

cd "$(dirname "$0")/.."

# ── Colors ──────────────────────────────────────────────
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# ── Load .env ───────────────────────────────────────────
if [ -f .env ]; then
  set -a
  source <(grep -v '^#' .env)
  set +a
fi

# ── Parse args ──────────────────────────────────────────
PLATFORM="both"
SKIP_REVIEW=false
VERSION=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --current)
      bash scripts/version_sync.sh --current
      exit 0
      ;;
    --screenshots)
      info "Uploading metadata + screenshots..."
      bash scripts/deploy_ios.sh metadata
      info "Done."
      exit 0
      ;;
    --skip-review)
      SKIP_REVIEW=true
      shift
      ;;
    ios|android)
      PLATFORM="$1"
      shift
      ;;
    *)
      if [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        VERSION="$1"
      else
        error "Unknown argument: $1"
      fi
      shift
      ;;
  esac
done

if [[ -z "$VERSION" ]]; then
  error "Version required. Usage: $0 <version> [ios|android] [--skip-review]"
fi

info "=== Store Deploy v${VERSION} ==="
info "Platform: ${PLATFORM}"
info "Submit for review: $(if $SKIP_REVIEW; then echo NO; else echo YES; fi)"
echo ""

# ── Step 1: Sync version ───────────────────────────────
info "Step 1/7: Syncing version to ${VERSION}"
bash scripts/version_sync.sh "$VERSION"
echo ""

# ── Step 2: iOS ────────────────────────────────────────
if [[ "$PLATFORM" == "both" || "$PLATFORM" == "ios" ]]; then
  info "Step 2/7: Building iOS IPA"
  flutter build ipa --release 2>&1 | tail -3
  echo ""

  info "Step 3/7: Uploading to TestFlight"
  cd ios
  bundle install --quiet 2>/dev/null || true
  bundle exec fastlane upload_testflight 2>&1 | grep -E "Using flutter|Successfully|Upload|finished|Error" | tail -5
  cd ..
  echo ""

  if ! $SKIP_REVIEW; then
    info "Step 4/7: Submitting for App Store Review"
    cd ios
    bundle exec fastlane release 2>&1 | grep -E "Successfully|finished|Error|review" | tail -5
    cd ..
    echo ""
  else
    info "Step 4/7: Skipping review (--skip-review)"
    echo ""
  fi
else
  info "Step 2-4: Skipping iOS (android only)"
  echo ""
fi

# ── Step 3: Android ────────────────────────────────────
if [[ "$PLATFORM" == "both" || "$PLATFORM" == "android" ]]; then
  info "Step 5/7: Building Android AAB"
  flutter build appbundle --release 2>&1 | tail -3
  echo ""

  info "Step 6/7: Uploading to Google Play (Internal → Alpha)"
  cd android
  bundle install --quiet 2>/dev/null || true
  bundle exec fastlane deploy 2>&1 | grep -E "Successfully|finished|Error|promote" | tail -5
  cd ..
  echo ""

  if ! $SKIP_REVIEW && [[ "$PLATFORM" == "android" ]]; then
    info "Step 6b: Promoting to Production"
    cd android
    bundle exec fastlane promote_production 2>&1 | grep -E "Successfully|finished|Error" | tail -3
    cd ..
    echo ""
  fi
else
  info "Step 5-6: Skipping Android (ios only)"
  echo ""
fi

# ── Step 7: Git commit ─────────────────────────────────
info "Step 7/7: Committing version bump"
git add pubspec.yaml android/app/build.gradle.kts ios/Runner/Info.plist ios/Podfile.lock pubspec.lock
if git diff --cached --quiet; then
  info "No version files changed (already committed)"
else
  git commit -m "chore(mobile): bump version to ${VERSION}

Co-Authored-By: ggcode <noreply@ggcode.dev>"
  info "Version bump committed."
fi
echo ""

info "=== Deploy complete! ==="
info "Version: ${VERSION}"
if [[ "$PLATFORM" == "both" || "$PLATFORM" == "ios" ]]; then
  info "iOS: Uploaded to TestFlight"
fi
if [[ "$PLATFORM" == "both" || "$PLATFORM" == "android" ]]; then
  info "Android: Uploaded to Alpha"
fi
if ! $SKIP_REVIEW && [[ "$PLATFORM" == "both" || "$PLATFORM" == "ios" ]]; then
  info "iOS: Submitted for App Store Review"
fi
