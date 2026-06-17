#!/usr/bin/env bash
set -euo pipefail

# Publishes the website (docs/site/) to a deployment branch.
#
# IMPORTANT: This script creates an ORPHAN branch containing ONLY the
# docs/site/ directory plus a generated download manifest. It does NOT
# copy the full repo or commit any binary files. Download links point
# to GitHub Releases URLs.
#
# This keeps the git repo from growing by ~1.5 GB per release.

target_branch=""
remote_url=""
source_label=""
declare -a asset_dirs=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target-branch)
      target_branch="${2:-}"
      shift 2
      ;;
    --remote-url)
      remote_url="${2:-}"
      shift 2
      ;;
    --asset-dir)
      asset_dirs+=("${2:-}")
      shift 2
      ;;
    # Kept for backward compatibility — no longer used.
    --preserve-latest)
      shift
      ;;
    --source-label)
      source_label="${2:-}"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

if [[ -z "${target_branch}" ]]; then
  echo "--target-branch is required" >&2
  exit 1
fi

if [[ -z "${remote_url}" ]]; then
  if [[ -z "${GITHUB_REPOSITORY:-}" || -z "${GITHUB_TOKEN:-}" ]]; then
    echo "--remote-url is required when GITHUB_REPOSITORY or GITHUB_TOKEN is unset" >&2
    exit 1
  fi
  remote_url="https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPOSITORY}.git"
fi

if [[ -z "${source_label}" ]]; then
  source_label="${GITHUB_SHA:-manual}"
fi

publish_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/ggcode-site-branch.XXXXXX")"
cleanup() {
  rm -rf "${publish_dir}"
}
trap cleanup EXIT

# Initialize orphan branch (no parent history).
git init -b "${target_branch}" "${publish_dir}" >/dev/null
git -C "${publish_dir}" remote add origin "${remote_url}"

# Railway deploys from this branch with Root Directory = docs/site.
# So all site files must live under docs/site/ in the orphan branch.
site_dir="${publish_dir}/docs/site"
mkdir -p "${site_dir}"

# Copy ONLY the site content — not the full repo.
if [[ -d docs/site ]]; then
  rsync -a --delete --exclude '.git' docs/site/ "${site_dir}/"
else
  echo "WARNING: docs/site/ not found, publishing empty site"
fi

# Copy install scripts so they're served from ggcode.dev/install.sh and /install.ps1
if [[ -d scripts/install ]]; then
  cp scripts/install/install.sh "${site_dir}/install.sh" 2>/dev/null || true
  cp scripts/install/install.ps1 "${site_dir}/install.ps1" 2>/dev/null || true
fi

# Generate a download manifest from release assets WITHOUT copying binaries.
# The manifest lists files and their GitHub Releases download URLs so the
# website can link directly to GitHub-hosted assets.
latest_dir="${site_dir}/downloads/latest"
mkdir -p "${latest_dir}"

if [[ ${#asset_dirs[@]} -gt 0 ]]; then
  include_release_asset() {
    local name
    name="$(basename "$1")"
    case "${name}" in
      checksums.txt|*.tar.gz|*.zip|*.deb|*.rpm|*.apk|*.ipk|*.pkg.tar.zst|*.pkg|*.msi|*.dmg|*.exe|*.AppImage)
        return 0
        ;;
    esac
    return 1
  }

  # Collect asset names (NOT file contents) for the manifest.
  asset_names=()
  for asset_dir in "${asset_dirs[@]}"; do
    if [[ ! -d "${asset_dir}" ]]; then
      continue
    fi
    while IFS= read -r -d '' file; do
      if include_release_asset "${file}"; then
        asset_names+=("$(basename "${file}")")
      fi
    done < <(find "${asset_dir}" -maxdepth 1 -type f -print0)
  done

  # Generate manifest.json with GitHub Releases URLs.
  release_tag="${source_label}"
  python3 - "${latest_dir}" "${release_tag}" "${asset_names[@]}" <<'PY'
import html
import json
import os
import sys

out_dir = sys.argv[1]
release_tag = sys.argv[2]
asset_names = sys.argv[3:]

repo_url = os.environ.get("GITHUB_REPOSITORY", "topcheer/ggcode")
dl_base = f"https://github.com/{repo_url}/releases/download/{release_tag}"

files = []
for name in sorted(asset_names):
    files.append({
        "name": name,
        "url": f"{dl_base}/{name}",
    })

with open(os.path.join(out_dir, "manifest.json"), "w", encoding="utf-8") as fh:
    # Include both "version" and "source" so the manifest can be consumed by
    # install.go (which expects {"version": "v1.3.71"}) and by humans/scripts.
    import re
    version = release_tag
    match = re.match(r'v?(\d+\.\d+\.\d+)', release_tag)
    if match:
        version = 'v' + match.group(1)
    json.dump({"version": version, "source": release_tag, "files": files}, fh, indent=2)
    fh.write("\n")

items = "\n".join(
    f'<li><a href="{html.escape(item["url"])}" rel="external">{html.escape(item["name"])}</a></li>'
    for item in files
)
page = f"""<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>ggcode latest downloads</title>
    <style>
      body {{ font-family: system-ui, sans-serif; margin: 2rem auto; max-width: 56rem; padding: 0 1rem; }}
      ul {{ padding-left: 1.25rem; }}
      li {{ margin: 0.5rem 0; }}
      code {{ background: #f3f3f3; padding: 0.1rem 0.3rem; border-radius: 0.25rem; }}
    </style>
  </head>
  <body>
    <h1>ggcode latest downloads</h1>
    <p>Version: <code>{html.escape(release_tag)}</code></p>
    <ul>
      {items}
    </ul>
  </body>
</html>
"""
with open(os.path.join(out_dir, "index.html"), "w", encoding="utf-8") as fh:
    fh.write(page)
PY
fi

# Generate railway.json under docs/site/ to force DOCKERFILE builder.
# This overrides Railway's default Railpack driver which fails to resolve
# the Root Directory path.
cat > "${site_dir}/railway.json" <<'RAILJSON'
{
  "$schema": "https://railway.com/railway.schema.json",
  "build": {
    "builder": "DOCKERFILE",
    "dockerfilePath": "Dockerfile"
  },
  "deploy": {
    "healthcheckPath": "/health",
    "healthcheckTimeout": 30
  }
}
RAILJSON

cd "${publish_dir}"
git add --all
if git diff --cached --quiet; then
  echo "No deployment branch changes to publish."
  exit 0
fi

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git commit -m "deploy: publish site from ${source_label}"
git push origin "${target_branch}" --force
