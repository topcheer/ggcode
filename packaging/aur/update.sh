#!/usr/bin/env bash
# Refresh PKGBUILD checksums for a new ggcode release.
# Usage: ./update.sh <version>   e.g. ./update.sh 1.3.248
set -euo pipefail

if [ $# -ne 1 ]; then
    echo "usage: $0 <version>" >&2
    exit 1
fi

VER="$1"
BASE="https://github.com/topcheer/ggcode/releases/download/v${VER}"
DIR="$(cd "$(dirname "$0")" && pwd)"
PKGBUILD="${DIR}/PKGBUILD"

fetch_sha() {
    curl -fL "$1" | shasum -a 256 | cut -d' ' -f1
}

X86_SHA="$(fetch_sha "${BASE}/ggcode_linux_x86_64.tar.gz")"
ARM_SHA="$(fetch_sha "${BASE}/ggcode_linux_arm64.tar.gz")"

upd() {
    # upd <perl_regex> - applied to PKGBUILD in place
    perl -0pi -e "$1" "${PKGBUILD}"
}

upd "s/(pkgver=)[0-9.]+/\${1}${VER}/"
upd "s/(pkgrel=)[0-9]+/\${1}1/"
upd "s{(/releases/download/)v[0-9.]+(/ggcode_linux_)}{\${1}v${VER}\${2}}g"
upd "s/(sha256sums_x86_64=\(\")([a-f0-9]{64}|PLACEHOLDER_X86_64_SHA256)(\"\))/\${1}${X86_SHA}\${3}/"
upd "s/(sha256sums_aarch64=\(\")([a-f0-9]{64}|PLACEHOLDER_AARCH64_SHA256)(\"\))/\${1}${ARM_SHA}\${3}/"

echo "PKGBUILD updated to v${VER}:"
echo "  x86_64  ${X86_SHA}"
echo "  aarch64 ${ARM_SHA}"
echo "Next: cd ${DIR} && makepkg --printsrcinfo > .SRCINFO && git commit"
