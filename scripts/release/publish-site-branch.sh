#!/usr/bin/env bash
set -euo pipefail

target_branch=""
remote_url=""
preserve_latest="false"
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
    --preserve-latest)
      preserve_latest="true"
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
existing_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/ggcode-site-existing.XXXXXX")"
cleanup() {
  rm -rf "${publish_dir}" "${existing_dir}"
}
trap cleanup EXIT

branch_exists="false"
if git ls-remote --exit-code --heads "${remote_url}" "${target_branch}" >/dev/null 2>&1; then
  branch_exists="true"
  git clone --depth 1 --branch "${target_branch}" --single-branch "${remote_url}" "${existing_dir}"
fi

git init -b "${target_branch}" "${publish_dir}" >/dev/null
git -C "${publish_dir}" remote add origin "${remote_url}"
rsync -a --delete --exclude '.git' ./ "${publish_dir}/"

latest_dir="${publish_dir}/docs/site/downloads/latest"
if [[ "${preserve_latest}" == "true" && -d "${existing_dir}/docs/site/downloads/latest" ]]; then
  mkdir -p "${publish_dir}/docs/site/downloads"
  rm -rf "${latest_dir}"
  cp -R "${existing_dir}/docs/site/downloads/latest" "${latest_dir}"
fi

include_release_asset() {
  local name
  name="$(basename "$1")"
  case "${name}" in
    checksums.txt|*.tar.gz|*.zip|*.deb|*.rpm|*.apk|*.ipk|*.pkg.tar.zst|*.pkg|*.msi)
      return 0
      ;;
  esac
  return 1
}

if [[ ${#asset_dirs[@]} -gt 0 ]]; then
  mkdir -p "${publish_dir}/docs/site/downloads"
  rm -rf "${latest_dir}"
  mkdir -p "${latest_dir}"
  for asset_dir in "${asset_dirs[@]}"; do
    if [[ ! -d "${asset_dir}" ]]; then
      continue
    fi
    while IFS= read -r -d '' file; do
      if include_release_asset "${file}"; then
        cp "${file}" "${latest_dir}/"
      fi
    done < <(find "${asset_dir}" -maxdepth 1 -type f -print0)
  done

  python3 - "${latest_dir}" "${source_label}" <<'PY'
import html
import json
import os
import sys

out_dir = sys.argv[1]
source_label = sys.argv[2]
files = []
for name in sorted(os.listdir(out_dir)):
    path = os.path.join(out_dir, name)
    if not os.path.isfile(path):
        continue
    if name in {"manifest.json", "index.html"}:
        continue
    files.append({"name": name, "size": os.path.getsize(path)})

with open(os.path.join(out_dir, "manifest.json"), "w", encoding="utf-8") as fh:
    json.dump({"source": source_label, "version": source_label, "files": files}, fh, indent=2)
    fh.write("\n")

items = "\n".join(
    f'<li><a href="./{html.escape(item["name"])}">{html.escape(item["name"])}</a> '
    f'<span>{item["size"]} bytes</span></li>'
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
      span {{ color: #666; margin-left: 0.5rem; font-size: 0.9rem; }}
      code {{ background: #f3f3f3; padding: 0.1rem 0.3rem; border-radius: 0.25rem; }}
    </style>
  </head>
  <body>
    <h1>ggcode latest downloads</h1>
    <p>Source: <code>{html.escape(source_label)}</code></p>
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

cd "${publish_dir}"
git add --all
if git diff --cached --quiet; then
  echo "No deployment branch changes to publish."
  exit 0
fi

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git commit -m "deploy: publish site branch from ${source_label}"
git push origin "${target_branch}" --force
