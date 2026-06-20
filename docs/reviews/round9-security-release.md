# Round 9 — Security, Release, Packaging, Cross-platform

**Scope**: full-codebase security; `.goreleaser.yaml`; `.github/workflows/`; `scripts/`; install wrappers (`cmd/ggcode-installer/`, `npm/`, `python/`); native packages (MSI, deb, rpm, apk, ipk, pkg.tar.zst); winget; mobile app-store compliance.

**Date**: 2026-05-29. Round 8 references: `docs/reviews/round8-security.md`, `docs/reviews/cross-platform-compat.md`, `security/SECURITY_AUDIT_REPORT.md`, `docs/design-decisions.md` (some "issues" are intentional).

---

## Security — Round 8 findings status

| ID | Title | Status | Evidence | Action |
|----|-------|--------|----------|--------|
| C-1 | Relay WS zero auth | **OPEN** | `ggcode-relay/relay.go:724-760`, `:16-18` | See Round 9 mobile-relay action plan |
| C-2 | WebUI WS `CheckOrigin: true` | **DESIGN-INTENDED** | `internal/webui/server_websocket.go:16-18`; intent doc: `docs/design-decisions.md:60-91` (127.0.0.1 + token auth) | None unless remote binding introduced |
| C-3 | `/nuke` unauthenticated | **OPEN** | `ggcode-relay/relay.go:738-760` | Require admin token or remove |
| C-4 | Config API key exposure | **PARTIAL** | `internal/webui/server_handlers.go:125,185,670-671` returns booleans only; underlying file still 0644 | Combine with H-20 fix |
| C-5 | Tunnel token = encryption key | **OPEN** | `internal/tunnel/crypto.go:13-25` | HKDF-derive enc key from token; separate auth |
| H-18 | WebUI auth token `==` comparison | **OPEN** | `internal/webui/auth.go:32-43` | `subtle.ConstantTimeCompare` |
| H-19 | DingTalk token in debug logs | **OPEN** | `internal/im/dingtalk_adapter.go` | Redact body/URL before logging |
| H-20 | Config file perms 0644 | **OPEN** | `internal/config/config_save.go:59-63,138-142` | Write `0600`; create parent dir `0700` |
| H-21 | Session temp files world-readable | **RESOLVED** | `internal/provider/model_discovery.go:326-339` writes 0600 in 0700 dir; no other world-readable session temp writes found | None |

---

## Cross-cutting security audits (Round 9)

### RESOLVED / LOW — MiMo vendor header forwarding

- **Files**: `internal/provider/vendor_headers.go:9-28`, `internal/provider/model_discovery.go:113-117`
- **Finding**: `api-key` header is only added when the parsed host **exactly** matches `xiaomimimo.com` or a subdomain (case-insensitive, hostname-based, not substring). Non-MiMo endpoints do **not** receive the header.
- **Status**: Safe. Keep exact host/suffix matching; don't switch to substring match in future refactors.

### PARTIAL — Model discovery cache hardening

- **Files**: `internal/provider/model_discovery.go:30-45, 263-344`
- **Finding**: Cached in `~/.ggcode/model_discovery_cache.json`, written `0600` in `0700` dir. **Does not** cache credentials. TTL 6h; stale entries purged on read. But: entries unbounded, no inter-process lock, lazy eviction only.
- **Action**: see Round 9 Go findings — add flock + size cap + prune-on-write.

### RESOLVED — Per-endpoint metrics

- **Files**: `internal/session/endpoint_stats.go:23-80`, `internal/metrics/metrics.go:5-28`
- **Finding**: Metrics store vendor/endpoint strings only; no API key fields. No secrets at rest in metrics.
- **Action**: keep `MetricEvent` secret-free; add a unit test that asserts the JSON encoding doesn't contain `api_key|token|secret`.

### RESOLVED — npm/pip wrappers binary integrity

- **Files**: `npm/lib/install.js:251-335`, `python/ggcode_release_installer/cli.py:177-186`
- **Finding**: Both wrappers download `checksums.txt` and verify SHA256 before install; HTTPS release URLs used.
- **Action**: keep checksum verification mandatory.

### PARTIAL — Relay 12h expiry state cleanup

- **Files**: `ggcode-relay/store.go:15-19`, `ggcode-relay/relay.go:255-260`
- **Finding**: Retention is 12h; session switch hydrates/clears room history based on session change. Could not fully confirm token + cursor + history rows are all purged on expiry.
- **Action**: add an explicit expiry sweep with assertions; integration test that asserts a deleted room leaves no rows in any table.

### PARTIAL — Hydrate on session ID change

- **Files**: `ggcode-relay/relay.go:255-260`
- **Finding**: On session change, room history is cleared and reloaded; risk of leaking prior-room data if room/session mapping is wrong.
- **Action**: bind history strictly to `(room_id, session_id)`; reject queries that don't match both.

---

## Release / CI quality

### PARTIAL — Release-blocking tests for v1.3.x

- **Files**: `.github/workflows/release.yml:23-39`, `.github/workflows/ci.yml:9-25`
- **Finding**: Release + CI run `go test`, `go vet`, `gofmt`. No explicit version-bump gate. 8 releases shipped in 3 days (v1.3.40 → v1.3.48).
- **Action**: add release-version smoke assertions + changelog/version consistency check (e.g., assert `docs/releases/<version>.md` exists; assert `npm/package.json` + `python/setup.py` + `desktop/.../version` all match the tag).

### RESOLVED — Package SHA256 sums published

- **Files**: `.goreleaser.yaml:40-42`, `scripts/release/smoke-installer.sh:43`
- **Finding**: GoReleaser emits `checksums.txt`; installer smoke tests consume it.
- **Action**: keep publishing checksums.

### PARTIAL — `make verify-ci` not wired into CI workflow

- **Files**: `Makefile:21-22`, `scripts/dev/verify-ci.sh:24-63`, `.github/workflows/ci.yml`
- **Finding**: `make verify-ci` exists and works locally, but CI calls direct commands. Local-CI parity is not enforced.
- **Action**: replace CI's individual commands with `make verify-ci`.

### PARTIAL — Post-release smoke per package

- **Files**: `scripts/release/smoke-installer.sh:39-75`, `.github/workflows/release.yml:343-486`
- **Finding**: Some installer smoke tests exist, but there's no single workflow step that **downloads every published package and runs `--version`**.
- **Action**: add a matrix job per OS × format (deb, rpm, apk, ipk, pkg.tar.zst, msi, pkg, brew) that installs the artifact and runs `ggcode --version`; fail the release on any mismatch.

### RESOLVED — gofmt / vet / tests blocking

- Both CI and release verify and gate on these.

---

## Cross-platform compatibility

### PARTIAL — Windows MSI install/upgrade/uninstall, ARM64/x64

- **Files**: `scripts/release/build-windows-msi.ps1:18-60`, `.github/workflows/release.yml:126-163`, `.github/packaging/windows/*.wxs`
- **Finding**: MSI builds amd64 + arm64. WiX authoring not verified for upgrade/uninstall cleanup, Defender flags, or registry/file-association handling.
- **Action**: audit `.wxs` upgrade table; add MajorUpgrade + uninstall cleanup tests; submit signed binaries to Microsoft for SmartScreen reputation seeding.

### RESOLVED — Linux desktop entries/icons/MIME (at script level)

- **Files**: `scripts/release/build-desktop-linux.sh:82-126, 129-174`
- **Finding**: deb/rpm packages include `.desktop`, icon, and metainfo assets at the build-script level.
- **Action**: see Round 9 desktop finding M — verify that goreleaser nfpm `contents:` actually emits these into the package.

### PARTIAL — macOS titlebar CGo code-signing / notarization

- **Files**: `desktop/ggcode-desktop/titlebar_darwin.go`, `.github/workflows/release.yml:165-221`
- **Finding**: Packaging builds signed + unsigned variants. Notarization / Gatekeeper behavior with the new CGo titlebar integration not exercised in CI.
- **Action**: add a Gatekeeper assessment job (`spctl --assess`) on signed `.app` post-build.

### PARTIAL — Mobile iOS compliance

- **Files**: `mobile/flutter/ios/Runner/PrivacyInfo.xcprivacy:1-55`, `mobile/flutter/ios/Runner/Info.plist:48-51`, `.github/workflows/mobile-release.yml:116-245`
- **Finding**: Privacy manifest exists. `ITSAppUsesNonExemptEncryption=false` declared. IDFA / privacy-review completeness beyond these files not verified.
- **Action**: verify each third-party SDK has a corresponding privacy manifest entry (mobile_scanner, MLKit, sqlite3, shared_preferences, url_launcher, wakelock_plus, package_info_plus).

### PARTIAL — Android target SDK compliance

- **Files**: `mobile/flutter/android/app/src/main/AndroidManifest.xml:1-32`, `.github/workflows/mobile-release.yml:53-114`
- **Finding**: Manifest minimal; targetSdk / version policy in Gradle not verified.
- **Action**: confirm `targetSdk` ≥ current Play Store requirement (35 as of 2025); audit permissions; verify no over-broad declarations.

---

## Recommended action items

| Priority | Item |
|----------|------|
| P0 | H-20 config file perms → `0600` (single-line fix, broadly impactful) |
| P0 | H-18 constant-time auth token comparison |
| P0 | H-19 redact DingTalk tokens in logs |
| P0 | Relay auth / TLS / rate limit (C-1, M-42, H-17) |
| P0 | C-5 tunnel auth ≠ encryption key |
| P1 | Wire `make verify-ci` into CI (parity + drift prevention) |
| P1 | Per-package `--version` smoke matrix |
| P1 | Release version-consistency gate |
| P1 | Model discovery cache flock + size cap |
| P1 | macOS Gatekeeper smoke job on `.app` |
| P2 | Audit WiX upgrade/uninstall + add SmartScreen seeding plan |
| P2 | iOS third-party SDK privacy-manifest sweep |
| P2 | Android targetSdk + permission audit |
| P3 | Relay expiry cleanup integration test (no leftover rows) |
| P3 | Metric secret-free unit test |
