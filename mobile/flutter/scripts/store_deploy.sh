#!/bin/bash
# store_deploy.sh — One-command mobile app store deployment
#
# Usage:
#   ./scripts/store_deploy.sh              # Deploy with current version (auto build number)
#   ./scripts/store_deploy.sh 1.4.0        # Bump version, then deploy
#   ./scripts/store_deploy.sh ios          # iOS only
#   ./scripts/store_deploy.sh android      # Android only
#   ./scripts/store_deploy.sh 1.4.0 ios    # Bump version, deploy iOS only
#   ./scripts/store_deploy.sh --current    # Show current version info
#   ./scripts/store_deploy.sh --tag        # Create git tag for current version
#   ./scripts/store_deploy.sh --release    # Submit latest TestFlight build for App Store Review
#
# Version logic:
#   - Version name (1.3.10): from latest git tag, or bump with argument
#   - Build number (2026052001): fully automatic, date + 2-digit sequence
#   - Same version multiple deploys = auto-increment build number
#
set -euo pipefail

cd "$(dirname "$0")/.."

# ── Colors ──────────────────────────────────────────────
GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; BLUE='\033[0;34m'; NC='\033[0m'
info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; exit 1; }
step()  { echo -e "\n${BLUE}━━━ $* ━━━${NC}"; }

# ── Load .env ───────────────────────────────────────────
if [ -f .env ]; then
  set -a; source <(grep -v '^#' .env); set +a
fi

# ── Get current version from git tags ──────────────────
get_latest_tag_version() {
  git tag --sort=-v:refname 2>/dev/null | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 | sed 's/^v//'
}

# ── Parse args ──────────────────────────────────────────
PLATFORM="both"
NEW_VERSION=""
ACTION="deploy"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --current|-c)
      bash scripts/version_sync.sh --current
      exit 0
      ;;
    --tag|-t)
      ACTION="tag"
      shift
      ;;
    --release|-r)
      ACTION="release"
      shift
      ;;
    ios|android)
      PLATFORM="$1"
      shift
      ;;
    [0-9]*.[0-9]*.[0-9]*)
      NEW_VERSION="$1"
      shift
      ;;
    *)
      fail "Unknown argument: $1\nUsage: $0 [version] [ios|android] [--current] [--tag]"
      ;;
  esac
done

# ── Determine version ──────────────────────────────────
if [[ -n "$NEW_VERSION" ]]; then
  VERSION="$NEW_VERSION"
  info "Version: ${VERSION} (manual bump)"
else
  VERSION=$(get_latest_tag_version)
  if [[ -z "$VERSION" ]]; then
    VERSION=$(grep '^version:' pubspec.yaml | sed 's/version: \([0-9.]*\)+.*/\1/')
  fi
  info "Version: ${VERSION} (from git tag)"
fi

# ── Tag action ─────────────────────────────────────────
if [[ "$ACTION" == "tag" ]]; then
  step "Creating git tag v${VERSION}"
  # Ensure version is synced first
  bash scripts/version_sync.sh "$VERSION"
  git add pubspec.yaml android/app/build.gradle.kts ios/Runner/Info.plist
  git diff --cached --quiet || git commit -m "chore(mobile): bump version to ${VERSION}

Co-Authored-By: ggcode <noreply@ggcode.dev>"
  git tag "v${VERSION}"
  info "Tagged v${VERSION} ✓"
  info "Push with: git push origin main --tags"
  exit 0
fi

# ── Release action ────────────────────────────────────
if [[ "$ACTION" == "release" ]]; then
  step "Submitting latest TestFlight build for App Store Review"
  cd ios && bundle install --quiet 2>/dev/null || true
  bundle exec fastlane release_latest 2>&1 | tail -30
  cd ..
  info "Submitted for App Store Review ✓"
  exit 0
fi

info "═══ Store Deploy v${VERSION} (${PLATFORM}) ═══"

# ── Pre-flight ─────────────────────────────────────────
step "Pre-flight"
command -v flutter >/dev/null || fail "flutter not found"
command -v bundle >/dev/null || fail "bundle not found"
if [[ "$PLATFORM" != "android" ]]; then
  [[ -n "${APP_STORE_KEY_ID:-}" ]] || fail "APP_STORE_KEY_ID not in .env"
fi
info "Checks OK"

# ── Step 1: Sync version (auto build number) ───────────
step "1/4: Sync version (auto build number)"
bash scripts/version_sync.sh "$VERSION"

# ── iOS ────────────────────────────────────────────────
if [[ "$PLATFORM" == "both" || "$PLATFORM" == "ios" ]]; then
  step "2a/4 [iOS]: Build IPA"
  flutter build ipa --release 2>&1 | tail -3
  [[ -f "build/ios/ipa/GGCode Mobile.ipa" ]] || fail "IPA build failed"

  step "2b/4 [iOS]: Upload → TestFlight → External Testing → Submit for Review (if needed)"
  cd ios && bundle install --quiet 2>/dev/null || true
  bundle exec fastlane deploy_external 2>&1 | tail -30
  cd ..
  info "iOS: TestFlight → External Testing ✓"
fi

# ── Android ────────────────────────────────────────────
if [[ "$PLATFORM" == "both" || "$PLATFORM" == "android" ]]; then
  step "3/4 [Android]: Build AAB → Internal → Closed Testing"
  cd android && bundle install --quiet 2>/dev/null || true
  bundle exec fastlane deploy 2>&1 | tail -30
  cd ..
  info "Android: Internal → Closed Testing ✓"
fi

# ── Step 4: Git commit ─────────────────────────────────
step "4/4: Commit"
git add pubspec.yaml android/app/build.gradle.kts ios/Runner/Info.plist
git diff --cached --quiet || {
  git commit -m "chore(mobile): deploy v${VERSION}

Co-Authored-By: ggcode <noreply@ggcode.dev>"
  info "Committed ✓"
}

echo ""
info "══════════════════════════════════════"
info "  Deploy complete!"
info "  Version: ${VERSION}"
[[ "$PLATFORM" == "both" || "$PLATFORM" == "ios" ]]     && info "  iOS:     External Testing ✓"
[[ "$PLATFORM" == "both" || "$PLATFORM" == "android" ]]  && info "  Android: Closed Testing ✓"
info "══════════════════════════════════════"
