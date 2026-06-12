# Review 55: Two-Round Full Application Review

Date: 2026-06-08

This document summarizes the main findings and recommended fixes from two read-only team review rounds. The reviews covered Go backend/core, desktop/Wails/frontend, mobile, WebUI, relay/tunnel, CI/CD, release automation, and SRE/security concerns.

No code changes were made during the review.

## Executive Summary

The most important issues fall into four areas:

1. **Security hardening**
   - Unsafe Markdown/SVG rendering in Wails and WebUI.
   - WebUI session ID/path validation gaps.
   - WebSocket `CheckOrigin` allowing all origins.
   - Request body size limits missing on WebUI JSON endpoints.

2. **Runtime correctness and lifecycle reliability**
   - `run_command` auto-background flow may call `cmd.Wait()` twice on the same process.
   - Cron persistence failures are silently ignored and may cause duplicate scheduled runs.
   - Subagent/swarm stream batcher shutdown is not idempotent.
   - Swarm can accept work after teammates are marked shutting down.

3. **Desktop/Wails performance and state consistency**
   - Wails stream event queue silently drops events when full.
   - Large sessions are loaded and rendered all at once with no pagination or virtual list.
   - Optimistic `SendMessage` failures leave frontend and backend state divergent.
   - Some Wails event cleanup uses global `EventsOff(eventName)`, which can remove other components' listeners.

4. **CI/release/SRE reliability**
   - CI and local verification use inconsistent integration test tags.
   - Release asset upload failures are downgraded to warnings.
   - Relay container/Railway persistence is incomplete.
   - GoReleaser checks and Homebrew SHA generation can mask failures.

## Priority Fix Plan

### P0 / High priority

1. Sanitize Wails Markdown/SVG rendering.
2. Validate and canonicalize WebUI session IDs before read/export/delete.
3. Fix `run_command` auto-background double-`Wait()` ownership.
4. Make cron persistence failures visible and prevent duplicate one-shot firing.
5. Stop silently dropping critical Wails stream events.
6. Make release asset upload failures fail the workflow.
7. Align CI and local `verify-ci` integration test behavior.
8. Fix Relay persistence paths for Docker/Railway deployments.

### P1 / Medium priority

1. Add WebUI/Relay WebSocket origin allowlists.
2. Add WebUI JSON request body size limits.
3. Make subagent/swarm stream batcher start/shutdown idempotent.
4. Reject sends to shutting-down swarm teammates.
5. Prevent stale session `index.json` updates after append-only writes.
6. Add pagination/virtualization for large desktop session history.
7. Fix Wails event cleanup to use `EventsOn()` unsubscribe callbacks.
8. Make GoReleaser check, Homebrew SHA calculation, and site manifest generation fail-safe.

### P2 / Low priority

1. Synchronize Wails `productVersion` with release version.
2. Reduce desktop status polling or make it event-driven.
3. Enforce mobile manual release version input against actual metadata.
4. Enable or evaluate Android release minify/resource shrink.
5. Improve Relay `/health` into `/live` and `/ready` checks.
6. Use npm provenance consistently.

---

## Round 1 Findings

### High: Unsafe Wails Markdown/SVG rendering can execute untrusted script

- **Files**:
  - `desktop/ggcode-desktop-wails/frontend/src/components/ChatView.tsx:46-65`
  - `desktop/ggcode-desktop-wails/frontend/src/components/ChatView.tsx:1211`
- **Problem**: `safeMarkdown()` returns `marked.parse(text)` directly and injects it with `dangerouslySetInnerHTML`. SVG segments are also inserted directly. `marked` does not sanitize HTML by default.
- **Impact**: Assistant output, tool results, or restored session history can contain malicious HTML/SVG. In Wails, frontend script execution may be able to call bound Go methods, making this higher risk than regular browser XSS.
- **Recommended fix**:
  - Use DOMPurify or `react-markdown` with `rehype-sanitize`.
  - Disable raw HTML by default.
  - Do not directly render raw SVG. If SVG support is required, sanitize with a strict SVG allowlist and remove event handlers, scripts, external references, and `foreignObject`.

### High: WebUI session ID validation gaps may allow path traversal

- **Files**: WebUI session read/export/delete handlers and session store call sites.
- **Problem**: Session IDs are reused in read/export/delete paths without sufficiently strict validation.
- **Impact**: A crafted ID containing traversal sequences could read, export, or delete unintended `.jsonl` files.
- **Recommended fix**:
  - Introduce one shared `validateSessionID` helper.
  - Only allow canonical session ID characters, e.g. `[A-Za-z0-9._-]`, or the project's stricter canonical format.
  - After path construction, apply `filepath.Clean` and verify the final path remains under the session root.
  - Add regression tests for `../`, encoded traversal, path separators, and absolute paths.

### High: CI runs integration tests while local verification excludes them

- **Files**:
  - `.github/workflows/ci.yml:22-25`
  - `scripts/dev/verify-ci.sh:39-47`
  - `tests/acp_integration/acp_integration_test.go:48-69`
- **Problem**: CI runs `go test -tags "goolm,integration" ./...`, while local verification runs `go test -tags "goolm,!integration" ./...` and clears provider API key environment variables.
- **Impact**: Developers cannot reproduce CI locally. PR CI can become flaky due to network, credentials, external services, or rate limits.
- **Recommended fix**:
  - Make default CI match local verification: `go test -tags "goolm,!integration" ./...`.
  - Move external/integration tests to a separate scheduled/manual/protected workflow.
  - Clarify and unify `integration` vs `integration_local` tag semantics.

### High: Relay persistence is not correctly configured for container/Railway deployments

- **Files**:
  - `ggcode-relay/store.go:17-20`
  - `ggcode-relay/store.go:412-419`
  - `ggcode-relay/Dockerfile:8-11`
  - `ggcode-relay/railway.json:5-10`
  - `ggcode-relay/relay.go:1518-1523`
- **Problem**: Relay prefers `/db/relay.db`, but the Dockerfile does not create `/db`, declare a volume, or set `GGCODE_RELAY_DB_PATH`. Railway also does not bind a persistent volume.
- **Impact**: Relay event history, session cursor data, and room state may be lost after restart/redeploy, weakening replay and recovery.
- **Recommended fix**:
  - Create `/db` in the Dockerfile.
  - Set `GGCODE_RELAY_DB_PATH=/db/relay.db`.
  - Declare `VOLUME ["/db"]`.
  - Bind a Railway persistent volume.
  - Add DB readiness checks.

### High: Release asset upload failures are downgraded to warnings

- **File**: `.github/workflows/release.yml:419-440`
- **Problem**: `gh release upload ... || echo "WARN: failed to upload $pattern"` lets the workflow succeed even if critical assets fail to upload.
- **Impact**: Releases can appear successful while missing installers or packages. Downstream channels such as winget, site downloads, or Homebrew Cask can point to missing assets.
- **Recommended fix**:
  - Fail fast on required upload failure.
  - Or collect failures and `exit 1` at the end.
  - Validate required release assets with `gh release view --json assets`.

### Medium: Wails `EventsOff(eventName)` cleanup can remove other components' listeners

- **Files**:
  - `desktop/ggcode-desktop-wails/frontend/src/components/Layout.tsx:112-113`, `:167-193`
  - `desktop/ggcode-desktop-wails/frontend/src/components/ShareDialog.tsx:26-36`
  - `desktop/ggcode-desktop-wails/frontend/src/components/IMManagement.tsx:114-115`
  - `desktop/ggcode-desktop-wails/frontend/src/components/MCPServers.tsx:97-98`
  - `desktop/ggcode-desktop-wails/frontend/src/components/ContextPanel.tsx:56-57`, `:75-79`
- **Problem**: Components subscribe to shared events but cleanup with global `EventsOff('event')`, removing all listeners for the event.
- **Impact**: Unmounting one component can break live updates in another.
- **Recommended fix**:
  - Use the unsubscribe callback returned by `EventsOn()`:
    ```ts
    const off = EventsOn('event', handler)
    return () => {
      if (typeof off === 'function') off()
    }
    ```

### Medium: WebUI Markdown links allow unsafe URL schemes

- **File**: `internal/webui/dist/index.html:1978-1989`
- **Problem**: Regex-based Markdown rendering injects `<a href="$2">` without URL scheme validation.
- **Impact**: `[click](javascript:alert(1))` can create an executable link.
- **Recommended fix**:
  - Allow only safe schemes such as `http:`, `https:`, and `mailto:`.
  - Escape href attributes correctly.
  - Prefer a maintained Markdown renderer plus sanitizer.

### Medium: WebUI JSON APIs lack request body size limits

- **Problem**: JSON request bodies can be decoded without `http.MaxBytesReader` or equivalent limits.
- **Impact**: Large requests can cause memory pressure or denial of service.
- **Recommended fix**:
  - Add a shared JSON decoding helper with size limits.
  - Return `413 Request Entity Too Large` when exceeded.

### Medium: Path sandbox write operations may be vulnerable to symlink/TOCTOU bypass

- **Problem**: Write paths can be validated before use, but a symlink can be swapped between validation and write.
- **Impact**: A path allowed by policy may write outside allowed directories.
- **Recommended fix**:
  - Revalidate canonical paths immediately before write.
  - Avoid following symlinks for sensitive writes where possible.
  - Use platform support such as `O_NOFOLLOW` when available.

### Medium: OpenAI provider HTTP client wrapping can lose timeout/configuration

- **Problem**: Wrapping an HTTP client can accidentally replace the client instead of cloning and only wrapping `Transport`.
- **Impact**: Timeout, redirect, jar, or proxy behavior may be lost, leading to hangs or inconsistent network behavior.
- **Recommended fix**:
  - Clone the original `http.Client` and preserve all fields.
  - Replace only the transport layer that needs wrapping.

### Medium: Relay HTTP server lacks base timeout and header limits

- **Files**:
  - `ggcode-relay/relay.go:1568-1575`
  - `ggcode-relay/relay.go:312-323`
- **Problem**: Server sets only `Addr` and `Handler`, without `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, or `MaxHeaderBytes`.
- **Impact**: Public deployments are more exposed to slowloris/slow header/idle connection exhaustion.
- **Recommended fix**:
  - Set sane server-level timeouts and header limits.
  - Treat upgraded WebSocket connections separately if needed.

### Medium: WebUI and Relay WebSocket `CheckOrigin` allow all origins

- **Files**:
  - `ggcode-relay/relay.go:1040`
  - `internal/webui/server_websocket.go:15-18`
- **Problem**: WebSocket upgraders return `true` for all origins.
- **Impact**: Any website can initiate browser-based WebSocket attempts. If credentials/tokens leak, this expands CSWSH risk.
- **Recommended fix**:
  - Add explicit allowed-origin checks.
  - Allow empty Origin only for non-browser clients if required.
  - Make relay origins configurable via environment variable.

### Medium: npm provenance configuration contradicts publish command

- **Files**:
  - `npm/package.json:14-17`
  - `.github/workflows/npm.yml:11-13`
  - `.github/workflows/npm.yml:49-52`
- **Problem**: `publishConfig.provenance=true` and workflow `id-token: write` are present, but publishing uses `--provenance=false`.
- **Impact**: npm package lacks expected provenance attestation.
- **Recommended fix**:
  - Publish with `--provenance`, or remove provenance claims and id-token permission if intentionally disabled.

### Medium: Wails product version is stale

- **Files**:
  - `desktop/ggcode-desktop-wails/wails.json:13-18`
  - `mobile/flutter/pubspec.yaml:1-4`
- **Problem**: Wails `productVersion` is `1.3.60`, while the project/mobile version is `1.3.67`.
- **Impact**: Desktop package metadata and About/version information can be wrong.
- **Recommended fix**:
  - Add Wails version sync to release scripts.
  - Add a release verification check for version consistency.

### Low: A2A API key comparison may leak length timing information

- **Problem**: API key comparison may not fully normalize length before constant-time compare.
- **Impact**: Low-risk timing side channel.
- **Recommended fix**:
  - Compare fixed-length hashes with `subtle.ConstantTimeCompare`.

### Low: WebUI auth token can be supplied via query string

- **Problem**: Query tokens can leak via history, logs, screenshots, and Referer.
- **Impact**: Token exposure risk is higher than header-based auth.
- **Recommended fix**:
  - Prefer Authorization headers.
  - Deprecate query token support or make it short-lived and local-only.

### Low: Desktop status bar polls backend every 500ms

- **File**: `desktop/ggcode-desktop-wails/frontend/src/components/Layout.tsx:135-162`
- **Problem**: Continuous calls to `App.GetModelInfo()` and `App.IsWorking()` create unnecessary IPC and React updates.
- **Impact**: Wasted CPU/bridge traffic, especially over long sessions.
- **Recommended fix**:
  - Use event-driven updates.
  - Keep a low-frequency fallback only when needed.

### Low: Android release build disables minify/resource shrink

- **File**: `mobile/flutter/android/app/build.gradle.kts:48-53`
- **Problem**: `isMinifyEnabled=false` and `isShrinkResources=false`.
- **Impact**: Larger package size and weaker basic static-analysis resistance.
- **Recommended fix**:
  - Evaluate enabling R8/resource shrink for production releases.
  - Add keep rules and verify plugins/crypto/scanner/websocket flows.

### Low: Relay healthcheck does not verify readiness

- **Files**:
  - `ggcode-relay/relay.go:1541-1543`
  - `ggcode-relay/railway.json:5-10`
- **Problem**: `/health` always returns 200 and does not check SQLite/disk/background tasks.
- **Impact**: Platform may route traffic to an instance whose DB or persistence layer is broken.
- **Recommended fix**:
  - Split `/live` and `/ready`.
  - Add DB ping/lightweight read or write checks and background task status.

---

## Round 2 Findings

Round 2 focused on finding issues not already covered in Round 1.

### High: Wails stream event queue silently drops events when full

- **Files**:
  - `desktop/ggcode-desktop-wails/app.go:88-107`
  - `desktop/ggcode-desktop-wails/app.go:194-213`
- **Problem**: `streamEvents` is a fixed `4096`-buffered channel. `emitStreamEvent()` uses non-blocking send and silently discards events on overflow.
- **Impact**: During large sessions, fast token streams, tool output bursts, or subagent/swarm activity, the frontend can permanently miss `text`, `tool_result`, `done`, `error`, or status events. UI can show incomplete text, stuck tool cards, or stale working state.
- **Recommended fix**:
  - Do not silently drop critical state events.
  - Coalesce high-frequency token events, but guarantee delivery of lifecycle events.
  - Track dropped count and notify frontend with a `stream_resync_required` event.
  - Frontend should call `GetSessionHistory()` to rebuild projection after resync notification.

### High: `run_command` auto-background can call `cmd.Wait()` twice

- **Files**:
  - `internal/tool/run_command.go:325-327`
  - `internal/tool/run_command.go:376-378`
  - `internal/tool/command_jobs.go:217-240`
- **Problem**: `executeWithAutoBackground` starts a waiter goroutine. On timeout, it hands the same running `*exec.Cmd` to `JobManager.AutoBackground`, which starts another waiter and calls `cmd.Wait()` again.
- **Impact**: `exec.Cmd.Wait()` is single-use. The background job can fail with `Wait was already called`, lose the true exit status, or report misleading completion/failure.
- **Recommended fix**:
  - Ensure exactly one component owns `Wait()`.
  - Transfer the original waiter/done channel into the background job.
  - Or create/adopt the job before process start and make the job manager the sole waiter.

### High: Cron persistence failures are ignored and may cause duplicate executions

- **Files**:
  - `internal/cron/scheduler.go:297-314`
  - `internal/cron/scheduler.go:153-195`
- **Problem**: Timer callbacks enqueue work, mutate memory, and call `s.save()`, but `save()` returns no error and silently ignores several read/write failures.
- **Impact**: If persistence fails, one-shot jobs can remain in the store and re-fire after restart. Recurring jobs may lose `NextFire` advancement and execute early or duplicate.
- **Recommended fix**:
  - Make `save()` return errors and log/surface failures.
  - Persist one-shot fired/completed state before enqueueing, or add robust retry logic.
  - Add tests for persistence failure behavior.

### Medium: `goreleaser check || true` masks invalid release configuration

- **File**: `.github/workflows/release.yml:69-70`
- **Problem**: GoReleaser config validation failure is ignored.
- **Impact**: Invalid release configuration can proceed into later steps and fail partially.
- **Recommended fix**:
  - Remove `|| true` for release workflows.
  - If temporary, gate it only in non-release dry runs with a clear comment.

### Medium: Homebrew SHA generation can checksum HTTP error bodies

- **Files**:
  - `.github/workflows/release.yml:527-533`
  - `.github/workflows/release.yml:613-618`
- **Problem**: SHA is computed with `curl -sL | sha256sum`. HTTP error pages can become a valid-looking checksum.
- **Impact**: Homebrew formulas/casks can be generated with invalid checksums, causing install failures.
- **Recommended fix**:
  - Use `curl -fsSL`.
  - Download to a temp file, verify status, file size, and expected format, then checksum.

### Medium: Site latest-download manifest excludes extensionless desktop binaries

- **Files**:
  - `scripts/release/publish-site-branch.sh:80-88`
  - `.github/workflows/release.yml:758-761`
- **Problem**: Release workflow passes desktop artifact directories, but manifest generation excludes extensionless binaries.
- **Impact**: Site latest-download metadata may omit valid desktop binaries.
- **Recommended fix**:
  - Explicitly include expected extensionless binaries in manifest generation.
  - Validate generated manifest against release assets.

### Medium: Mobile release concurrency can cancel active store deployments

- **File**: `.github/workflows/mobile-release.yml:19-21`
- **Problem**: `cancel-in-progress: true` can cancel an in-flight tag-triggered deployment.
- **Impact**: App Store / Play Store uploads or promotions may be interrupted mid-release.
- **Recommended fix**:
  - Do not cancel in-progress runs for tag-triggered releases.
  - Include tag/version in concurrency group.

### Medium: Subagent/swarm stream batcher lifecycle is not idempotent

- **Files**:
  - `internal/subagent/manager.go:313-315`
  - `internal/swarm/manager.go:571-584`
  - `internal/swarm/manager.go:598-600`
- **Problem**: Shutdown closes channels directly without `sync.Once`; `StartStreamBatcher()` can start multiple goroutines.
- **Impact**: Repeated interrupt/exit cleanup, tests, or manager reuse can panic or duplicate flush work.
- **Recommended fix**:
  - Add `sync.Once` or guarded state for start and shutdown.
  - Make `Shutdown()` safe to call multiple times.

### Medium: Swarm can accept work after teammates are shutting down

- **Files**:
  - `internal/swarm/manager.go:350-371`
  - `internal/swarm/manager.go:390-395`
- **Problem**: `CancelAll()` marks teammates as shutting down and cancels contexts, but inboxes remain open. `SendToTeammate` does not check teammate status.
- **Impact**: Work can be accepted and then never processed, creating silent lost tasks after interrupt/cancel.
- **Recommended fix**:
  - Reject sends unless teammate status is active/acceptable.
  - Close or drain inboxes safely during shutdown.

### Medium: Append-only session helpers may update `index.json` from stale snapshots

- **File**: `internal/session/store.go:820-845`
- **Problem**: `AppendMessageToDisk` and `AppendTunnelEventToDisk` append durable records, then call `s.updateIndex(ses)` using a potentially stale `Session` snapshot.
- **Impact**: JSONL data can be correct while `index.json` has stale ordering, counts, timestamps, or activity metadata.
- **Recommended fix**:
  - Compute index metadata under the store lock from the append operation.
  - Or require and validate a fresh immutable snapshot from callers.

### Medium: Desktop large session history is loaded and rendered all at once

- **Files**:
  - `desktop/wailskit/chat.go:908-915`
  - `desktop/ggcode-desktop-wails/frontend/src/components/ChatView.tsx:253-300`
  - `desktop/ggcode-desktop-wails/frontend/src/components/ChatView.tsx:900-1079`
- **Problem**: Backend returns the entire history slice; frontend calls `setMessages(history)` and renders with `messages.map(...)` without pagination or virtualization.
- **Impact**: Large sessions can cause big Wails serialization payloads, React diff spikes, Markdown/Mermaid render stalls, white screens, and input lag.
- **Recommended fix**:
  - Add paginated/curser-based session history APIs.
  - Use virtualized lists on the frontend.
  - Lazy-render or collapse long tool output, reasoning, Markdown, and Mermaid content.

### Medium: Optimistic `App.SendMessage` failures leave frontend/backend state divergent

- **File**: `desktop/ggcode-desktop-wails/frontend/src/components/ChatView.tsx:740-763`
- **Problem**: Frontend appends the user message and clears input before `App.SendMessage(text)` succeeds. On failure, it only appends an error message.
- **Impact**: User sees a message as sent even if backend never received it. A later history reload can make the message disappear.
- **Recommended fix**:
  - Add `pending`, `sent`, and `failed` message states.
  - Mark sent only after backend ack.
  - On failure, restore input or provide retry.

### Low: Manual mobile release version input is not enforced

- **Files**:
  - `.github/workflows/mobile-release.yml:92-103`
  - `.github/workflows/mobile-release.yml:182-193`
- **Problem**: Manual input `version` is displayed but not checked against `pubspec.yaml` or native build metadata.
- **Impact**: Operators can publish one version while believing they published another.
- **Recommended fix**:
  - Compare workflow input against Flutter and native metadata.
  - Fail when they differ.

---

## Suggested Implementation Batches

### Batch 1: Security and data safety

- Sanitize Wails/WebUI Markdown and SVG rendering.
- Validate WebUI session IDs in one shared helper.
- Add WebSocket Origin checks.
- Add WebUI JSON body limits.
- Harden path sandbox writes against symlink/TOCTOU.

### Batch 2: Runtime correctness

- Refactor `run_command` auto-background ownership so only one goroutine calls `Wait()`.
- Make cron persistence return/report errors and prevent repeated one-shot jobs.
- Make subagent/swarm batcher lifecycle idempotent.
- Reject sends to shutting-down teammates.
- Fix append-only session index updates.

### Batch 3: Desktop performance and state consistency

- Replace Wails stream-event silent drop with backpressure/coalescing/resync.
- Add session history pagination and frontend virtualization.
- Add pending/sent/failed states to optimistic user messages.
- Fix Wails `EventsOff` cleanup patterns.
- Reduce or replace status polling with event-driven state.

### Batch 4: CI/release/SRE

- Align CI and local verify test tags.
- Fail release on required asset upload errors.
- Fix Relay Docker/Railway persistent DB configuration.
- Remove `goreleaser check || true`.
- Harden Homebrew SHA generation.
- Fix site latest-download manifest completeness.
- Adjust mobile release concurrency and version validation.
