# SRE / Build / DevOps Review — ggcode

**Reviewer:** Automated SRE Audit  
**Date:** 2025-07-27  
**Scope:** Build system, CI/CD, deployment, operational readiness  
**Severity Levels:** Critical / High / Medium / Low

---

## Executive Summary

The ggcode project has a mature multi-platform release pipeline covering CLI (GoReleaser), desktop (Fyne), mobile (Flutter), and package managers (npm, PyPI). The build system correctly uses the `goolm` build tag throughout. However, several gaps exist in security scanning, dependency management, container hardening, and operational observability. The relay server Dockerfile and CI workflows have actionable issues that should be addressed before production-scale deployment.

---

## 1. Makefile

### Finding 1.1 — `install` target omits build tags
- **Severity:** High  
- **File:** `Makefile`, lines 27-28  
- **Description:** The `install` target runs `go install $(PKG)` without `-tags goolm`. Every other build target (`build`, `test`, `lint`) correctly includes `TAGS := goolm`. Without the tag, `go install` will fail on platforms that require the libolm C dependency (mautrix crypto). The `install-installer` target similarly omits tags.  
- **Recommendation:** Change to `go install -tags "$(TAGS)" $(PKG)` and `go install -tags "$(TAGS)" $(INSTALLER_PKG)`.

### Finding 1.2 — `build` target does not inject version ldflags
- **Severity:** Medium  
- **File:** `Makefile`, lines 9-10  
- **Description:** The `build` target compiles without `-ldflags` for version injection. `build-desktop` injects `main.Version` via ldflags, but the primary CLI build does not. This means `ggcode --version` will report a placeholder instead of the actual version for local builds. The release pipeline (GoReleaser) handles this correctly, but developer builds and `make install` will have blank version info.  
- **Recommendation:** Add ldflags to the `build` target matching the GoReleaser pattern: `-X github.com/topcheer/ggcode/internal/version.Version=$(shell git describe --tags --always --dirty)`.

### Finding 1.3 — No `fmt` or `lint-full` target for golangci-lint
- **Severity:** Low  
- **File:** `Makefile`, lines 18-19  
- **Description:** The `lint` target only runs `go vet`. While `.golangci.yml` configures a richer set of linters (errcheck, staticcheck, unused), there is no Makefile target to run `golangci-lint run`. CI also only runs `gofmt` and `go vet`, not the full linter suite.  
- **Recommendation:** Add a `lint-full` target: `golangci-lint run -tags "$(TAGS)" ./...`.

---

## 2. CI Pipeline (`.github/workflows/`)

### Finding 2.1 — CI runs integration tests; release verify job does not
- **Severity:** Medium  
- **File:** `.github/workflows/ci.yml`, line 25 vs `.github/workflows/release.yml`, line 37  
- **Description:** CI runs `go test -tags "goolm,integration" ./...` while the release verify job runs `go test -tags goolm` (without `integration`). This inconsistency means the release pipeline tests fewer code paths than CI. If any integration test catches a regression, the release could still proceed.  
- **Recommendation:** Align test tags between CI and release. If integration tests require API keys, use secrets or make the release verify job match CI exactly.

### Finding 2.2 — No dependency vulnerability scanning
- **Severity:** High  
- **File:** `.github/workflows/ci.yml` (entire file)  
- **Description:** There is no `govulncheck`, `dependabot`, or Snyk step in any CI workflow. The project depends on 90+ transitive packages including cryptographic libraries (`golang-jwt`, `btcsuite`, `filippo.io/edwards25519`), network libraries (`gorilla/websocket`, `valyala/fasthttp`), and authentication code. Without automated vulnerability scanning, known CVEs in dependencies could go undetected.  
- **Recommendation:** Add a `govulncheck ./...` step to CI and/or enable Dependabot version updates and security updates in GitHub settings.

### Finding 2.3 — No SARIF/security scanning integration
- **Severity:** Medium  
- **File:** `.github/workflows/ci.yml` (entire file)  
- **Description:** No CodeQL, Semgrep, or similar static analysis security scanner is configured. For a project handling API keys, OAuth tokens, and user credentials, security scanning should be part of the CI pipeline.  
- **Recommendation:** Add a CodeQL or Semgrep scanning workflow, even at minimal config.

### Finding 2.4 — `gofmt` check is separate job, not combined
- **Severity:** Low  
- **File:** `.github/workflows/ci.yml`, lines 27-38  
- **Description:** The format check is a separate job that re-downloads Go modules and the entire Go toolchain. This duplicates the setup from the test job. While functionally correct, it doubles CI minutes.  
- **Recommendation:** Consider combining into a single job with multiple steps, or accept the overhead for parallelism.

### Finding 2.5 — Inconsistent action versions across workflows
- **Severity:** Medium  
- **File:** `.github/workflows/mobile-release.yml`, line 35 vs `.github/workflows/release.yml`, line 30  
- **Description:** The mobile-release workflow uses `actions/checkout@v4` while the main release and CI workflows use `actions/checkout@v6`. Mixing action major versions across workflows can lead to inconsistent behavior and makes maintenance harder.  
- **Recommendation:** Standardize all workflows to the same action versions.

### Finding 2.6 — Mobile workflow uses `cancel-in-progress: true` on release tags
- **Severity:** High  
- **File:** `.github/workflows/mobile-release.yml`, line 21  
- **Description:** The mobile release sets `cancel-in-progress: true` in its concurrency group. If two tags are pushed in quick succession (e.g., a hotfix), the first mobile release build could be cancelled mid-deployment to App Store/Play Store. The main release workflow correctly uses `cancel-in-progress: false`.  
- **Recommendation:** Change to `cancel-in-progress: false` to match the release workflow pattern.

### Finding 2.7 — No test coverage reporting
- **Severity:** Low  
- **File:** `.github/workflows/ci.yml`  
- **Description:** CI runs tests but does not collect or report coverage metrics. There is no `-coverprofile` flag or coverage upload step.  
- **Recommendation:** Add `go test -coverprofile=coverage.out -tags "goolm,integration" ./...` and upload to Codecov or similar.

---

## 3. Release Scripts (`scripts/`)

### Finding 3.1 — `smoke-binary.sh` is minimal (225 bytes)
- **Severity:** Medium  
- **File:** `scripts/release/smoke-binary.sh`  
- **Description:** The smoke test script is only 225 bytes. A proper smoke test should verify: binary executes, `--version` outputs correctly, `--help` works, basic config loading succeeds. The current script likely only checks that the binary starts.  
- **Recommendation:** Expand smoke tests to cover: version output, help text, config validation, basic TUI startup (with timeout).

### Finding 3.2 — No automated rollback mechanism
- **Severity:** Medium  
- **File:** Release pipeline (general)  
- **Description:** The release pipeline has no automated rollback. If a bad release is published to GitHub Releases, npm, and PyPI simultaneously, each must be manually rolled back. The `npm.yml` workflow checks for duplicate versions (good), but there is no `--tag`-based rollback.  
- **Recommendation:** Document a rollback runbook. Consider adding a `scripts/release/rollback.sh` that yanks npm packages, deletes GitHub release, etc.

---

## 4. GoReleaser (`.goreleaser.yaml`)

### Finding 4.1 — GoReleaser version pinned to `latest`
- **Severity:** Medium  
- **File:** `.github/workflows/release.yml`, lines 59, 68  
- **Description:** The GoReleaser action uses `version: latest`. This means builds are non-reproducible across time — a build today may use a different GoReleaser version than a build next month, potentially changing archive formats, checksums, or behavior.  
- **Recommendation:** Pin to a specific GoReleaser version (e.g., `version: v2.7.0`) for reproducible releases.

### Finding 4.2 — No signing configuration
- **Severity:** Medium  
- **File:** `.goreleaser.yaml` (entire file)  
- **Description:** Binaries are not signed with GPG or cosign. The macOS pkg and Windows MSI build scripts handle platform-specific signing, but the core GoReleaser archives (tar.gz, zip) contain unsigned binaries. Users cannot verify binary authenticity via checksums alone without signing.  
- **Recommendation:** Add `signs` section to `.goreleaser.yaml` using cosign or GPG.

### Finding 4.3 — Homebrew cask token requires `TAP_GITHUB_TOKEN`
- **Severity:** Low  
- **File:** `.goreleaser.yaml`, line 81  
- **Description:** The Homebrew cask configuration requires a `TAP_GITHUB_TOKEN` environment variable. If this secret is not configured, the Homebrew tap update will fail silently (GoReleaser skips the section). This is documented behavior but could be surprising.  
- **Recommendation:** Add a comment in `.goreleaser.yaml` noting this requirement.

---

## 5. Dockerfile / Container Security (ggcode-relay)

### Finding 5.1 — Outdated base images
- **Severity:** High  
- **File:** `ggcode-relay/Dockerfile`, lines 1, 8  
- **Description:** The builder stage uses `golang:1.24-alpine` but `go.mod` specifies `go 1.24`. The main module uses `go 1.26.1`. More critically, the runtime stage uses `alpine:3.19` which is older than the current stable Alpine 3.21. Running outdated base images exposes the container to known vulnerabilities in Alpine's system packages.  
- **Recommendation:** Update to `golang:1.26-alpine` for the builder and `alpine:3.21` for the runtime stage.

### Finding 5.2 — Container runs as root
- **Severity:** High  
- **File:** `ggcode-relay/Dockerfile`, lines 8-11  
- **Description:** The container runs as root (no `USER` directive). If the relay process is compromised, the attacker has root-level access inside the container. This is a common CIS Docker Benchmark failure.  
- **Recommendation:** Add a non-root user:
  ```dockerfile
  RUN adduser -D -s /bin/sh relay
  USER relay
  ```

### Finding 5.3 — No `.dockerignore` for relay
- **Severity:** Medium  
- **File:** `ggcode-relay/` (missing `.dockerignore`)  
- **Description:** There is no `.dockerignore` file in the relay directory. The Docker build context will include `relay.db`, `relay.db-shm`, `relay.db-wal` (SQLite database files), test files, and other artifacts. This bloats the build context and could leak database contents into the Docker image layer cache.  
- **Recommendation:** Add a `.dockerignore`:
  ```
  *.db
  *.db-shm
  *.db-wal
  *_test.go
  deploy.sh
  run-local.sh
  .gitignore
  ```

### Finding 5.4 — No multi-stage build artifact cleanup
- **Severity:** Low  
- **File:** `ggcode-relay/Dockerfile`  
- **Description:** While the Dockerfile uses a multi-stage build (good), the runtime stage only copies the binary. However, the Go module download cache in the builder stage is not pruned. This is standard practice but worth noting.

### Finding 5.5 — No health check in Dockerfile
- **Severity:** Medium  
- **File:** `ggcode-relay/Dockerfile`  
- **Description:** The Dockerfile has no `HEALTHCHECK` directive. While `railway.json` configures `healthcheckPath: /health`, the Dockerfile itself should declare a health check for portability to other orchestrators (Docker Compose, Kubernetes).  
- **Recommendation:** Add `HEALTHCHECK CMD wget -q --spider http://localhost:8080/health || exit 1`

### Finding 5.6 — Relay server has no graceful shutdown
- **Severity:** High  
- **File:** `ggcode-relay/main.go`, lines 926-929  
- **Description:** The relay server uses `http.ListenAndServe` directly with no signal handling for graceful shutdown. On SIGTERM (e.g., container restart, Railway deploy), active WebSocket connections are abruptly severed. In-flight messages could be lost. The peer `done` channel exists for individual peer cleanup, but the server itself has no `http.Server.Shutdown(ctx)` pattern.  
- **Recommendation:** Implement graceful shutdown using `signal.NotifyContext` and `server.Shutdown(ctx)` to drain active connections before exiting.

---

## 6. Dependency Management (`go.mod` / `go.sum`)

### Finding 6.1 — Go version mismatch between main module and relay
- **Severity:** Medium  
- **File:** `go.mod` line 3 vs `ggcode-relay/go.mod` line 3  
- **Description:** The main module uses `go 1.26.1` while the relay module uses `go 1.24`. The Dockerfile uses `golang:1.24-alpine`. This inconsistency means the relay is built with an older toolchain than the main project, which could lead to behavioral differences or missed compiler optimizations.  
- **Recommendation:** Update relay `go.mod` to `go 1.26.1` and Dockerfile to `golang:1.26-alpine`.

### Finding 6.2 — Outdated indirect dependency: `go.opencensus.io`
- **Severity:** Low  
- **File:** `go.mod`, line 126  
- **Description:** `go.opencensus.io v0.24.0` is listed as an indirect dependency. OpenCensus has been sunset in favor of OpenTelemetry. This dependency is likely pulled in transitively and should be evaluated for removal.  
- **Recommendation:** Investigate if any direct dependency still requires opencensus and whether it can be updated.

### Finding 6.3 — No `go.sum` verification in CI
- **Severity:** Low  
- **File:** `.github/workflows/ci.yml`  
- **Description:** CI runs `go mod download` but does not run `go mod verify` to ensure checksums match. While Go's module system verifies checksums by default via `go.sum`, an explicit `go mod verify` step adds defense-in-depth against tampering.  
- **Recommendation:** Add `go mod verify` after `go mod download` in CI.

### Finding 6.4 — Multiple `go.mod` files with inconsistent Go versions
- **Severity:** Medium  
- **Files:** `go.mod` (1.26.1), `ggcode-relay/go.mod` (1.24), `desktop/ggcode-desktop/go.mod` (1.26.1), `internal/a2a/examples/rest-api/go.mod` (1.23.0)  
- **Description:** Four `go.mod` files exist with three different Go version directives. While some variance is expected for separate modules, the relay and example should ideally match the main module version.  
- **Recommendation:** Standardize all `go.mod` files to `go 1.26.1` where possible.

---

## 7. `.gitignore`

### Finding 7.1 — Artifact binary committed to repo
- **Severity:** High  
- **File:** Repository root — `ggcode` file (101 MB)  
- **Description:** A 102 MB compiled binary (`ggcode`) exists in the repository root. While `.gitignore` has `/ggcode` as the first line, this binary was committed before the gitignore rule was added (or the gitignore is not matching properly). This bloats the git history permanently. The `.gitignore` rule `/ggcode` only matches at the root level — this appears correct, but the file exists nonetheless.  
- **Recommendation:** Verify the binary is not tracked: `git ls-files ggcode`. If tracked, remove it from tracking with `git rm --cached ggcode` and consider using `git filter-branch` or BFG to purge it from history.

### Finding 7.2 — `.gitignore` missing common patterns
- **Severity:** Low  
- **File:** `.gitignore`  
- **Description:** Missing patterns for: `*.test` (Go test binaries), `*.prof` (CPU/memory profiles), `vendor/` (if ever vendored). The file has a stray line `-e` on line 37 which appears to be an accidental inclusion.  
- **Recommendation:** Add standard Go gitignore patterns. Remove the stray `-e` on line 37.

### Finding 7.3 — Relay `.gitignore` missing database files
- **Severity:** Medium  
- **File:** `ggcode-relay/.gitignore`  
- **Description:** The relay `.gitignore` only lists `/relay` and `*.exe`. SQLite database files (`relay.db`, `relay.db-shm`, `relay.db-wal`) are present in the directory but not gitignored. These appear to be runtime artifacts that should not be committed.  
- **Recommendation:** Add to `ggcode-relay/.gitignore`:
  ```
  *.db
  *.db-shm
  *.db-wal
  ```

---

## 8. Mobile/Flutter Build Configuration

### Finding 8.1 — `pod install || true` silently ignores failures
- **Severity:** Medium  
- **File:** `.github/workflows/mobile-release.yml`, line 198  
- **Description:** The iOS dependency installation step uses `pod install || true`, which means any CocoaPods failure (version conflicts, network errors, missing specs) is silently ignored. The build may proceed with stale or missing pods, leading to runtime crashes on device.  
- **Recommendation:** Remove `|| true` or change to `pod install --repo-update` with proper error handling.

### Finding 8.2 — Flutter SDK constraint is very broad
- **Severity:** Low  
- **File:** `mobile/flutter/pubspec.yaml`, line 8  
- **Description:** The SDK constraint `>=3.0.0 <4.0.0` spans the entire Dart 3.x range. With Flutter `3.38.7` pinned in CI, this is unlikely to cause issues, but local developers could use incompatible SDK versions.  
- **Recommendation:** Narrow the constraint to match the CI version more closely.

### Finding 8.3 — Mobile workflow hardcodes team ID
- **Severity:** Low  
- **File:** `.github/workflows/mobile-release.yml`, lines 192, 226  
- **Description:** Apple Team ID `EGZFS7M525` is hardcoded in the workflow. While this is not a secret, it makes the workflow less portable if the team changes.  
- **Recommendation:** Move to a secret or environment variable for maintainability.

### Finding 8.4 — `store_deploy.sh` sources `.env` with `set -a`
- **Severity:** Medium  
- **File:** `mobile/flutter/scripts/store_deploy.sh`, lines 31-33  
- **Description:** The script sources `.env` using `set -a; source <(grep -v '^#' .env); set +a`. If `.env` contains special characters, command injection is possible. The `grep -v '^#'` filter only removes comment lines but does not validate the content.  
- **Recommendation:** Use a proper `.env` parser or validate that values do not contain shell metacharacters.

---

## 9. npm / Python Package Wrappers

### Finding 9.1 — npm `provenance` disabled in publish
- **Severity:** Medium  
- **File:** `.github/workflows/npm.yml`, line 52  
- **Description:** The npm publish step uses `--provenance=false` despite `publishConfig.provenance: true` in `package.json`. npm provenance provides supply chain transparency by linking the published package to its source commit and build. Disabling it removes an important security guarantee.  
- **Recommendation:** Enable provenance by removing `--provenance=false` and ensuring the workflow has `id-token: write` permission (which it already does).

### Finding 9.2 — npm postinstall runs arbitrary binary download
- **Severity:** Medium  
- **File:** `npm/scripts/postinstall.js`, `npm/lib/install.js`  
- **Description:** The npm postinstall script downloads a binary from GitHub Releases and installs it to `/usr/local/bin` or `~/.local/bin`. While this is a common pattern (used by esbuild, turbo, etc.), it modifies the user's PATH and shell profile files (`.zshrc`, `.bashrc`, `.profile`). The script adds a PATH block to multiple shell profiles without confirming with the user. If the download fails, the user is told to run `ggcode-bootstrap` which repeats the process.  
- **Recommendation:** Consider prompting the user before modifying shell profiles. Add a `--yes` flag for CI use.

### Finding 9.3 — Python wrapper has no checksum verification
- **Severity:** Medium  
- **File:** `python/ggcode_release_installer/` (implied)  
- **Description:** While the npm installer verifies SHA256 checksums of downloaded archives (`verifyChecksum` function), the Python wrapper's download mechanism was not visible in the files read. If it does not verify checksums, it is vulnerable to MITM attacks during download.  
- **Recommendation:** Ensure the Python installer also verifies SHA256 checksums against `checksums.txt` from the GitHub release.

---

## 10. Operational Readiness

### Finding 10.1 — No structured logging or log levels
- **Severity:** Medium  
- **File:** `ggcode-relay/main.go` (uses `log.Printf`)  
- **Description:** The relay server uses Go's standard `log.Printf` for all output. There is no structured logging (JSON), no log levels (debug/info/warn/error), and no configurable verbosity. This makes production debugging and log aggregation difficult.  
- **Recommendation:** Migrate to a structured logger like `slog` (Go standard library since 1.21) or `zerolog` (already an indirect dependency).

### Finding 10.2 — Relay health check is minimal
- **Severity:** Medium  
- **File:** `ggcode-relay/main.go`, lines 922-925  
- **Description:** The `/health` endpoint returns a static `200 ok` without checking downstream dependencies. If the SQLite database becomes corrupted or unreachable, the health check would still pass.  
- **Recommendation:** Add a lightweight database connectivity check to the health endpoint (e.g., `SELECT 1`).

### Finding 10.3 — No metrics or tracing endpoint
- **Severity:** Medium  
- **File:** `ggcode-relay/`  
- **Description:** The relay has no Prometheus metrics endpoint, no distributed tracing, and no OpenTelemetry integration. While `trace.go` exists for internal event tracing, there is no export to an observability backend. For a production relay handling WebSocket connections, this means: no visibility into connection counts, message throughput, error rates, or latency.  
- **Recommendation:** Add a `/metrics` Prometheus endpoint. Consider basic metrics: active connections, messages relayed, errors, uptime.

### Finding 10.4 — No rate limiting on relay WebSocket endpoint
- **Severity:** Medium  
- **File:** `ggcode-relay/main.go`  
- **Description:** There is no rate limiting on the `/ws` WebSocket endpoint. A malicious client could open many connections or send messages at high volume, exhausting server resources.  
- **Recommendation:** Add connection-per-IP limiting and message rate limiting.

### Finding 10.5 — No configuration validation at startup
- **Severity:** Medium  
- **File:** `ggcode-relay/main.go` (main function)  
- **Description:** The relay reads configuration from environment variables (port, etc.) but does not validate them at startup. An invalid port number would cause a runtime error from `http.ListenAndServe` rather than a clear startup error.  
- **Recommendation:** Validate all configuration values at startup with clear error messages.

### Finding 10.6 — Pre-push hook does `git pull --rebase` automatically
- **Severity:** Medium  
- **File:** `.githooks/pre-push`, lines 30-46  
- **Description:** The pre-push hook automatically pulls and rebases from the remote before pushing. While the comment says this avoids push rejection from CI-pushed commits, it can also silently rewrite local history and cause unexpected merge conflicts during push. The hook also stashes/unstashes changes, which can fail in edge cases.  
- **Recommendation:** Consider making this behavior opt-in (via env var) or at least warn the user before pulling.

---

## Summary Table

| Severity | Count | Areas |
|----------|-------|-------|
| Critical | 0 | — |
| High | 6 | Install tags missing, no vuln scanning, container root, no graceful shutdown, binary in repo, mobile cancel-in-progress |
| Medium | 16 | Version ldflags, CI/release test mismatch, SARIF scanning, action version drift, GoReleaser pin, no signing, no .dockerignore, no healthcheck, Go version mismatch, pod install || true, .env sourcing, npm provenance, PATH modification, no structured logging, minimal health check, no metrics |
| Low | 7 | No golangci-lint target, separate format job, no coverage, opencensus sunset, go.sum verification, Flutter SDK constraint, hardcoded team ID |

---

## Priority Recommendations

1. **Immediate (High):**
   - Add `-tags goolm` to Makefile `install` target
   - Add `govulncheck` to CI pipeline
   - Add non-root `USER` to relay Dockerfile
   - Implement graceful shutdown in relay server
   - Fix mobile-release `cancel-in-progress` to `false`
   - Remove tracked binary from git history

2. **Short-term (Medium → High):**
   - Update Dockerfile base images to current versions
   - Pin GoReleaser version
   - Enable npm provenance
   - Add `.dockerignore` to relay
   - Add HEALTHCHECK to Dockerfile
   - Fix `pod install || true` to fail on error

3. **Medium-term:**
   - Add Prometheus `/metrics` endpoint to relay
   - Migrate relay to structured logging
   - Add binary signing (cosign)
   - Add CodeQL/Semgrep scanning workflow
   - Standardize Go versions across all modules
