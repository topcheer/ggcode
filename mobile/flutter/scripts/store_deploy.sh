#!/bin/bash
# store_deploy.sh — One-command mobile app store deployment
#
# Usage:
#   ./scripts/store_deploy.sh 1.3.9                  # Both platforms
#   ./scripts/store_deploy.sh 1.3.9 ios              # iOS only
#   ./scripts/store_deploy.sh 1.3.9 android          # Android only
#   ./scripts/store_deploy.sh --current              # Show current version
#
# iOS:  sync → build → upload TestFlight → wait → promote External Testing
# Android: sync → build → upload Internal → wait → promote Closed Testing
#
set -euo pipefail

cd "$(dirname "$0")/.."

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; BLUE='\033[0;34m'; CYAN='\033[0;36m'; NC='\033[0m'
info()   { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()   { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()   { echo -e "${RED}[FAIL]${NC} $*"; exit 1; }
step()   { echo -e "\n${BLUE}━━━ $* ━━━${NC}"; }

# ── Load .env ───────────────────────────────────────────
if [ -f .env ]; then
  set -a; source <(grep -v '^#' .env); set +a
fi

# ── Parse args ──────────────────────────────────────────
PLATFORM="both"; VERSION=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --current) bash scripts/version_sync.sh --current; exit 0 ;;
    ios|android) PLATFORM="$1"; shift ;;
    [0-9]*.[0-9]*.[0-9]*) VERSION="$1"; shift ;;
    *) fail "Unknown: $1" ;;
  esac
done
[[ -z "$VERSION" ]] && fail "Usage: $0 <version> [ios|android]"

info "═══ Store Deploy v${VERSION} (${PLATFORM}) ═══"

# ── Pre-flight ─────────────────────────────────────────
step "Pre-flight"
command -v flutter >/dev/null || fail "flutter not found"
command -v bundle >/dev/null || fail "bundle not found"
[[ "$PLATFORM" != "android" ]] && { [[ -n "${APP_STORE_KEY_ID:-}" ]] || fail "APP_STORE_KEY_ID not in .env"; }
[[ "$PLATFORM" != "ios" ]]     && { [[ -f "android/secrets/playstore-service-account.json" ]] || fail "Android service account missing"; }
info "Checks OK"

# ── Step 1: Sync version ───────────────────────────────
step "1/4: Sync version"
bash scripts/version_sync.sh "$VERSION"

# ── iOS ────────────────────────────────────────────────
if [[ "$PLATFORM" == "both" || "$PLATFORM" == "ios" ]]; then
  step "2a/4 [iOS]: Build IPA"
  flutter build ipa --release 2>&1 | tail -3
  [[ -f "build/ios/ipa/GGCode Mobile.ipa" ]] || fail "IPA build failed"

  step "2b/4 [iOS]: Upload → TestFlight → External Testing"
  cd ios && bundle install --quiet 2>/dev/null || true
  # upload_testflight: uploads pre-built IPA
  # promote_external: waits for processing + adds to external group
  bundle exec fastlane deploy_external 2>&1 | tail -20
  cd ..
  info "iOS: TestFlight → External Testing ✓"
fi

# ── Android ────────────────────────────────────────────
if [[ "$PLATFORM" == "both" || "$PLATFORM" == "android" ]]; then
  step "3/4 [Android]: Build AAB → Internal → Closed Testing"
  cd android && bundle install --quiet 2>/dev/null || true
  # deploy: build_aab → upload_internal → wait 60s → promote_alpha (with retry)
  bundle exec fastlane deploy 2>&1 | tail -15
  cd ..
  info "Android: Internal → Closed Testing ✓"
fi

# ── Step 4: Git commit ─────────────────────────────────
step "4/4: Commit"
git add pubspec.yaml android/app/build.gradle.kts ios/Runner/Info.plist ios/Podfile.lock pubspec.lock
git diff --cached --quiet || {
  git commit -m "chore(mobile): bump version to ${VERSION}

Co-Authored-By: ggcode <noreply@ggcode.dev>"
  info "Committed ✓"
}

echo ""
info "══════════════════════════════════════"
info "  v${VERSION} deployed!"
[[ "$PLATFORM" == "both" || "$PLATFORM" == "ios" ]]     && info "  iOS:     External Testing ✓"
[[ "$PLATFORM" == "both" || "$PLATFORM" == "android" ]]  && info "  Android: Closed Testing ✓"
info "══════════════════════════════════════"
