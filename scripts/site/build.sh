#!/bin/sh

set -eu

src_dir="docs/site"
out_dir=".site-build"

rm -rf "$out_dir"
mkdir -p "$out_dir"
cp -R "$src_dir"/. "$out_dir"/

asset_version="${RAILWAY_GIT_COMMIT_SHA:-${SOURCE_COMMIT:-${GITHUB_SHA:-}}}"
if [ -z "$asset_version" ]; then
  asset_version="$(git rev-parse --short=12 HEAD 2>/dev/null || date +%s)"
fi
asset_version="$(printf '%s' "$asset_version" | cut -c1-12)"

find "$out_dir" -type f -name '*.html' -exec sed -i.bak "s/__SITE_ASSET_VERSION__/${asset_version}/g" {} +
find "$out_dir" -type f -name '*.bak' -delete
