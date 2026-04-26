# ggcode release process

This document is the operator playbook for publishing a new `ggcode` release. It covers the local prep, the exact Git flow, what every GitHub Actions workflow does, which files must be updated, how the site branch is refreshed, and the common failure modes.

## 1. Release model

The repository uses a **tag-driven release flow**:

1. Prepare the release commit on `main`.
2. Push `main`.
3. Create and push a tag named `vX.Y.Z`.
4. GitHub Actions publishes binaries, OS packages, wrapper packages, and the static download mirror.

The main release workflow is `.github/workflows/release.yml`. It triggers on:

- `push.tags: ["v*"]` for real releases
- `workflow_dispatch` for manual packaging / dry-run style jobs

## 2. Files that usually change in a normal release

At a minimum, check these files before tagging:

| Purpose | File |
| --- | --- |
| GitHub release notes body | `docs/releases/vX.Y.Z.md` |
| Release notes index pointer | `docs/releases/README.md` |
| Project quick reference pointer | `GGCODE.md` |
| npm wrapper version | `npm/package.json` |
| Python wrapper version | `python/pyproject.toml` |

Notes:

- The Go CLI version embedded in binaries comes from the Git tag through GoReleaser ldflags, not from a hard-coded source file.
- If a tag-specific notes file does not exist, `scripts/release/render-notes.sh` falls back to a generated notes body based on commit subjects.
- Do **not** edit an older release note file in place; create a new `docs/releases/vX.Y.Z.md`.

## 3. Local pre-release checklist

### 3.1 Update release notes

Copy the template or a recent release file and create:

```bash
docs/releases/vX.Y.Z.md
```

Recommended structure:

1. `# ggcode vX.Y.Z`
2. `## Highlights`
3. `## Fixes and improvements`
4. `## Upgrade notes`
5. `## Compare`

The compare link format is:

```text
https://github.com/topcheer/ggcode/compare/vPREV...vNEXT
```

### 3.2 Update version references

For a normal release, bump these version references together:

1. `npm/package.json` → `"version": "X.Y.Z"`
2. `python/pyproject.toml` → `version = "X.Y.Z"`
3. `GGCODE.md` → latest documented release pointer
4. `docs/releases/README.md` → current release notes pointer

### 3.3 Run CI-aligned local validation

Use the repository’s CI-equivalent check:

```bash
make verify-ci
```

That runs `scripts/dev/verify-ci.sh`, which does:

1. `gofmt -l .` cleanliness check
2. `go mod download`
3. `go build -o /tmp/ggcode ./cmd/ggcode`
4. `go vet ./...`
5. `go test -tags=!integration ./...`

Important details:

- Integration tests are intentionally skipped by the CI-aligned script.
- The script unsets `ZAI_API_KEY`, `GGCODE_ZAI_API_KEY`, and `ZAI_MODEL`.
- The script also clears inherited `GIT_*` environment variables so nested test repos behave like CI.

### 3.4 Know what the pre-commit hook will do

If `.githooks/pre-commit` is installed, `git commit` will:

1. `gofmt -w` all staged `.go` files
2. re-stage those files
3. run `./scripts/dev/verify-ci.sh`

So a commit may fail even after you already ran checks if something changed between runs.

## 4. Exact release command flow

The intended operator flow is:

```bash
git add <release files>
git commit -m "release: vX.Y.Z"
git push origin main
git tag vX.Y.Z
git push origin vX.Y.Z
```

Why this order matters:

1. The release commit must exist on `main`.
2. The tag is what triggers the release workflow.
3. Pushing the tag before the release commit is on `main` is easy to avoid and makes the history harder to reason about.

## 5. What the release workflow does

The main workflow is `.github/workflows/release.yml`.

### 5.1 `verify`

Runs on Ubuntu before anything else:

1. `go mod download`
2. `go build -o /tmp/ggcode-release ./cmd/ggcode`
3. `go test ./...`
4. `go vet ./...`

This is stricter than `make verify-ci` because it runs `go test ./...` without the `!integration` tag filter.

### 5.2 `release`

If `verify` succeeds:

1. installs Syft
2. installs GoReleaser
3. runs `goreleaser check`
4. runs:
   - `goreleaser release --clean` for tag-driven releases
   - `goreleaser release --snapshot --clean --skip=publish` for manual `workflow_dispatch`
5. renders release notes using `scripts/release/render-notes.sh`
6. updates the GitHub Release body with `gh release edit`
7. uploads the `dist/` directory as a workflow artifact named `goreleaser-dist`

### 5.3 Smoke tests

After the release artifacts exist, the workflow runs:

- `release-smoke-linux`
- `release-smoke-macos`
- `release-smoke-windows`
- `release-smoke-installer`

These jobs unpack the built artifacts and run smoke scripts from `scripts/release/`.

### 5.4 Extra installer artifacts

If all smoke tests pass, the workflow additionally builds:

1. `release-macos-pkg`
   - runs `scripts/release/build-macos-pkg.sh`
   - uploads `.pkg` files to the GitHub Release
2. `release-windows-msi`
   - installs WiX 4
   - runs `scripts/release/build-windows-msi.ps1`
   - uploads `.msi` files to the GitHub Release

### 5.5 Site deployment branch refresh

Finally, `publish-site-release-branch` runs and pushes release assets into the deployment branch:

```text
copilot/site-release-update
```

It calls:

```bash
./scripts/release/publish-site-branch.sh \
  --target-branch "copilot/site-release-update" \
  --asset-dir dist \
  --asset-dir dist-site/macos-pkg \
  --asset-dir dist-site/windows-msi \
  --source-label "${GITHUB_REF_NAME}"
```

That script rebuilds `docs/site/downloads/latest/` and publishes:

- `checksums.txt`
- archive files (`.tar.gz`, `.zip`)
- Linux packages (`.deb`, `.rpm`, `.apk`, `.ipk`, `.pkg.tar.zst`)
- macOS `.pkg`
- Windows `.msi`
- generated `manifest.json`
- generated `index.html`

The published `manifest.json` uses the tag as both:

- `source`
- `version`

## 6. What GoReleaser publishes

The GoReleaser config lives in `.goreleaser.yaml`.

### 6.1 Binary matrix

The main CLI binary is built from:

```text
./cmd/ggcode
```

Platforms:

- `linux`
- `darwin`
- `windows`

Architectures:

- `amd64`
- `arm64`

### 6.2 Embedded version metadata

GoReleaser injects:

- `internal/version.Version={{ .Version }}`
- `internal/version.Commit={{ .Commit }}`
- `internal/version.Date={{ .Date }}`

### 6.3 Archive naming

Archives are emitted like:

- `ggcode_linux_x86_64.tar.gz`
- `ggcode_linux_arm64.tar.gz`
- `ggcode_darwin_x86_64.tar.gz`
- `ggcode_darwin_arm64.tar.gz`
- `ggcode_windows_x86_64.zip`
- `ggcode_windows_arm64.zip`

### 6.4 Additional packages

GoReleaser also builds native packages via NFPM:

- `.deb`
- `.rpm`
- `.apk`
- `.ipk`
- Arch Linux package (`.pkg.tar.zst`)

It also generates:

- `checksums.txt`
- SBOMs for archive artifacts

## 7. Wrapper package publishing

The Git tag triggers two separate wrapper-package workflows.

### 7.1 npm wrapper

Workflow:

```text
.github/workflows/npm.yml
```

Behavior:

1. reads `npm/package.json`
2. skips publish if the version starts with `0.0.0`
3. skips publish if that exact version already exists on npm
4. runs `npm pack --quiet`
5. runs `npm publish --access public --provenance=false`

Important:

- If `npm/package.json` is not bumped, the workflow will skip or fail to give you the intended new wrapper release.

### 7.2 Python wrapper

Workflow:

```text
.github/workflows/publish-pypi.yml
```

Behavior:

1. reads `python/pyproject.toml`
2. skips publish if the version starts with `0.0.0`
3. skips publish if that exact version already exists on PyPI
4. installs `build`
5. runs `python3 -m build` in `python/`
6. publishes `python/dist` with `pypa/gh-action-pypi-publish`

Important:

- If `python/pyproject.toml` is not bumped, PyPI will not receive the new wrapper version.

## 8. Static site publishing outside a release

The repository also has `.github/workflows/site-release.yml`.

This is separate from the normal release flow and is used when:

1. `docs/site/**` changes on `main`, or
2. someone manually dispatches the workflow

It publishes the static site branch while preserving the existing `docs/site/downloads/latest` directory unless explicitly rebuilding from release artifacts.

## 9. How release notes are chosen

Release notes are resolved by `scripts/release/render-notes.sh`.

Resolution order:

1. If `docs/releases/vX.Y.Z.md` exists, use it as-is.
2. Otherwise:
   - find the previous tag
   - generate a compare link
   - generate a filtered changelog from commit subjects

The fallback changelog excludes commits whose subjects start with:

- `docs:`
- `ci:`
- `chore:`
- `style:`

## 10. Recommended operator checklist

Use this checklist for every real release:

1. Finish code changes.
2. Create `docs/releases/vX.Y.Z.md`.
3. Bump `npm/package.json`.
4. Bump `python/pyproject.toml`.
5. Update `GGCODE.md` release pointer.
6. Update `docs/releases/README.md` current pointer.
7. Run `make verify-ci`.
8. Review `git status`.
9. Commit `release: vX.Y.Z`.
10. Push `main`.
11. Create and push tag `vX.Y.Z`.
12. Monitor GitHub Actions — **all workflows must reach `completed` / `success` before the release is considered done**:
    - `Release` (verify → release → smoke tests → macos-pkg → windows-msi → publish-site-release-branch)
    - `CI` (build → vet → test)
    - `npm`
    - `Publish PyPI`
    - `CodeQL` (security scan)
13. If any workflow fails:
    - `gh run view <run-id> --log-failed` to identify root cause
    - Fix the issue locally, commit, push to `main`
    - **Delete the tag** (`git push origin :refs/tags/vX.Y.Z && git tag -d vX.Y.Z`) and **re-tag** on the fix commit
    - Re-monitor all workflows from step 12
14. Confirm the release assets exist on GitHub (`gh release view vX.Y.Z`).
15. Confirm `https://ggcode.dev/downloads/latest/manifest.json` reflects the new tag.

## 11. Recommended monitoring commands

Useful commands after pushing a tag:

```bash
gh run list --limit 10
gh run list --workflow Release --limit 5
gh run watch <run-id>
gh release view vX.Y.Z
```

If a job fails and you need details:

```bash
gh run view <run-id> --log-failed
```

## 12. Common failure modes

### 12.1 Forgot to bump wrapper versions

Symptom:

- GitHub Release succeeds
- npm / PyPI workflow skips publish or does not publish the expected version

Fix:

- bump `npm/package.json` and `python/pyproject.toml` before tagging

### 12.2 Release notes point at the wrong version

Symptom:

- `GGCODE.md` or `docs/releases/README.md` still points to the previous release

Fix:

- update both pointers as part of the release commit

### 12.3 Local checks pass, commit still fails

Symptom:

- `git commit` fails during pre-commit

Cause:

- staged `.go` files were reformatted
- or CI-aligned validation failed in the hook

Fix:

- inspect the hook output
- re-stage any gofmt changes
- rerun `make verify-ci`

### 12.4 Tag exists but the release content is wrong

Symptom:

- the workflow publishes an unintended release body or old wrapper versions

Cause:

- the release commit was incomplete before tagging

Fix:

- the safest path is usually a follow-up patch release with a new tag

### 12.5 CI or Release workflow fails on pre-existing vet/lint issues

Symptom:

- `go vet ./...` fails on `sync.Mutex` copy-by-value or similar pre-existing warnings
- Release workflow's "Verify release inputs" step fails even though your code is clean

Cause:

- pre-existing issues in the codebase that CI's `go vet ./...` catches

Fix:

- fix the root cause (e.g. change `sync.Mutex` value fields to `*sync.Mutex` pointers)
- commit the fix, push to `main`
- delete and re-create the tag on the fix commit:

```bash
# Remove old tag locally and remotely
git tag -d vX.Y.Z
git push origin :refs/tags/vX.Y.Z

# Re-tag on the fix commit
git tag vX.Y.Z
git push origin vX.Y.Z
```

- re-monitor all workflows until every one reaches `completed` / `success`

## 13. Manual packaging runs

The `Release` workflow supports `workflow_dispatch` with `package_version`.

In manual mode:

- GoReleaser runs in snapshot mode
- publish is skipped
- release-note editing is skipped
- the job is best treated as a packaging / validation path, not a real release

## 14. Source of truth

If this document and the automation disagree, trust the code in:

1. `.github/workflows/release.yml`
2. `.github/workflows/npm.yml`
3. `.github/workflows/publish-pypi.yml`
4. `.github/workflows/site-release.yml`
5. `.goreleaser.yaml`
6. `scripts/dev/verify-ci.sh`
7. `scripts/release/render-notes.sh`
8. `scripts/release/publish-site-branch.sh`
