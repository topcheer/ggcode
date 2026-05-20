#!/bin/bash
# version_sync.sh — Sync version numbers across mobile Flutter project
#
# Usage:
#   ./scripts/version_sync.sh 1.3.9              # Set version, auto-generate build number
#   ./scripts/version_sync.sh 1.3.9 --dry-run    # Preview without writing
#   ./scripts/version_sync.sh --current           # Print current version info
#
# Build number format: YYYYMMDDN (date + daily sequence, e.g. 202605201)
# The script reads the current build number from pubspec.yaml, and if today's
# date prefix matches, increments the sequence digit; otherwise starts at 1.
#
set -euo pipefail

cd "$(dirname "$0")/.."

PUBSPEC="pubspec.yaml"
GRADLE="android/app/build.gradle.kts"
DRY_RUN=false

# ── Helpers ──────────────────────────────────────────────

die() { echo "ERROR: $*" >&2; exit 1; }

# Generate build number: YYYYMMDD + sequence
generate_build_number() {
	local today
	today=$(date +%Y%m%d)

	# Read current build number from pubspec.yaml
	local current_build
	current_build=$(grep '^version:' "$PUBSPEC" | sed 's/version: [0-9.]*+//' | tr -d '[:space:]')

	if [[ "$current_build" =~ ^${today}([0-9]+)$ ]]; then
		# Same day — increment sequence
		local seq=${BASH_REMATCH[1]}
		seq=$((seq + 1))
		echo "${today}${seq}"
	else
		# New day — start at 1
		echo "${today}1"
	fi
}

# ── Current version info ────────────────────────────────

show_current() {
	local line
	line=$(grep '^version:' "$PUBSPEC")
	local ver=${line#version: }
	local name=${ver%%+*}
	local build=${ver#*+}

	local gradle_vc
	gradle_vc=$(grep 'versionCode' "$GRADLE" | head -1 | sed 's/.*= *//' | tr -d ' ')
	local gradle_vn
	gradle_vn=$(grep 'versionName' "$GRADLE" | head -1 | sed 's/.*= *"//' | sed 's/".*//')

	echo "pubspec.yaml:     version ${ver}"
	echo "build.gradle.kts: versionCode=${gradle_vc} versionName=${gradle_vn}"

	if [[ "$name" != "$gradle_vn" ]]; then
		echo "⚠  Version name mismatch: pubspec=${name} gradle=${gradle_vn}"
	fi
	if [[ "$build" != "$gradle_vc" ]]; then
		echo "⚠  Version code mismatch: pubspec=+${build} gradle=${gradle_vc}"
	fi
}

# ── Sync ─────────────────────────────────────────────────

sync_version() {
	local version_name="$1"
	local build_number

	build_number=$(generate_build_number)

	local new_spec="${version_name}+${build_number}"

	echo "→ Setting version: ${new_spec}"
	echo "  pubspec.yaml:     version: ${new_spec}"
	echo "  build.gradle.kts: versionCode = ${build_number}, versionName = \"${version_name}\""

	if [[ "$DRY_RUN" == true ]]; then
		echo "(dry run — no files changed)"
		return
	fi

	# Update pubspec.yaml
	if [[ "$(uname)" == "Darwin" ]]; then
		sed -i '' "s/^version: .*/version: ${new_spec}/" "$PUBSPEC"
	else
		sed -i "s/^version: .*/version: ${new_spec}/" "$PUBSPEC"
	fi

	# Update build.gradle.kts
	if [[ "$(uname)" == "Darwin" ]]; then
		sed -i '' "s/versionCode = [0-9]*/versionCode = ${build_number}/" "$GRADLE"
		sed -i '' "s/versionName = \"[^\"]*\"/versionName = \"${version_name}\"/" "$GRADLE"
	else
		sed -i "s/versionCode = [0-9]*/versionCode = ${build_number}/" "$GRADLE"
		sed -i "s/versionName = \"[^\"]*\"/versionName = \"${version_name}\"/" "$GRADLE"
	fi

	echo "✓ Version synced."
}

# ── Main ─────────────────────────────────────────────────

if [[ $# -eq 0 ]]; then
	echo "Usage: $0 <version> [--dry-run]"
	echo "       $0 --current"
	exit 1
fi

if [[ "$1" == "--current" ]]; then
	show_current
	exit 0
fi

if [[ "$*" == *"--dry-run"* ]]; then
	DRY_RUN=true
fi

VERSION_NAME="$1"

# Validate version format (X.Y.Z)
if ! [[ "$VERSION_NAME" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	die "Invalid version format '${VERSION_NAME}'. Expected X.Y.Z (e.g. 1.3.9)"
fi

sync_version "$VERSION_NAME"
