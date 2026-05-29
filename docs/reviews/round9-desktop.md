# Round 9 — Desktop (Fyne) + macOS native titlebar

**Scope**: `desktop/ggcode-desktop/` (Go + Fyne), `desktop/markdownx/`.

**Date**: 2026-05-29. Round 8 reference: `docs/reviews/round8-desktop.md`.

---

## Round 8 findings — verified status

| ID | Title | Status | Evidence | Fix |
|----|-------|--------|----------|-----|
| C-7 | Predictable temp files | **OPEN** | `desktop/ggcode-desktop/chat_view.go:228-293`, `desktop/ggcode-desktop/main.go:34-35` — fixed `ggcode-clipboard-paste.png`, `ggcode-icon.png` | Switch to `os.CreateTemp()` with random suffix; in-memory icon path |
| M-35 | Recursive goroutine chain (pending-message drain) | **OPEN** | `desktop/ggcode-desktop/agent_bridge.go:709-723` | Serialize queued sends; don't spawn new send path from completion |
| M-36 | Unbounded HTTP read from kroki/mermaid | **OPEN** | `desktop/markdownx/render.go:297-307, 310-321` | Wrap with `io.LimitReader` (e.g., 8 MiB cap) |
| M-39 | `thinkingW` widget race | **OPEN** | `desktop/ggcode-desktop/chat_view.go:609-634` — goroutine reads while UI thread writes | Confine to main thread or guard with `sync.Mutex` / `atomic.Value` |
| M-40 | IM HTTP client no timeout | **RESOLVED** | `desktop/ggcode-desktop/im_bridge.go:170-176, 248-271` — bounded timeouts now | None |

---

## New findings (Round 9)

### M — macOS titlebar ignores system appearance

- **Severity**: Medium
- **Files**: `desktop/ggcode-desktop/titlebar_darwin.go:14-25, 90-105`
- **Description**: `configureUnifiedTitlebar()` forces `NSAppearanceNameDarkAqua` unconditionally. Light-mode users and system mode flips don't track correctly. Combined with the v1.3.47 themed-rectangle theme refresh, the titlebar visibly desyncs from the chat body when the user toggles themes.
- **Fix**: derive appearance name from current Fyne theme variant (light vs dark) and reapply on theme flip; also subscribe to macOS system appearance change via `NSDistributedNotificationCenter`.

### M — Titlebar CGo helper has unsafe lifetime assumptions

- **Severity**: Medium
- **Files**: `desktop/ggcode-desktop/titlebar_darwin.go:140-143, 189-191`
- **Description**: `C.free()` is used on `C.CString()` allocations, but the preamble does **not** include `<stdlib.h>`. Works because CGo synthesizes a declaration, but is brittle across CGo versions. Additionally, `RunNative` closures capture window context and call into Obj-C without an explicit main-thread guarantee — Fyne's driver may call the closure off the main thread in some paths.
- **Fix**:
  - Add `// #include <stdlib.h>` to the CGo preamble.
  - Wrap Obj-C calls in `dispatch_async(dispatch_get_main_queue(), ^{ ... })` or assert main-thread via `[NSThread isMainThread]`.

### M — `themedRectangle.Refresh()` is re-entrant and closure-heavy

- **Severity**: Medium
- **Files**: `desktop/ggcode-desktop/chat_view.go:39-57`
- **Description**: Each message bubble allocates its own `themedRectangle` instance carrying a `refreshFn` closure. `Refresh()` calls the closure, which may itself call `Refresh()` on the same canvas tree — re-entry risk under rapid theme toggles. Per-bubble closure footprint grows linearly with chat length.
- **Fix**:
  - Make `Refresh()` a pure data recompute (lookup colors from theme; assign).
  - Share one `themedRectangle` "style" struct + a small index/key instead of per-instance closures.

### M — Theme-switch refresh can be expensive/flickery

- **Severity**: Medium (Low if window count = 1)
- **Files**: `desktop/ggcode-desktop/app.go:1282-1299`
- **Description**: Theme change refreshes every window's content immediately. With onboarding + metrics window + chat window open, this retriggers full layout passes simultaneously, causing visible flicker. Especially noticeable on macOS where the new native titlebar appearance update + chat refresh fight for the same frame.
- **Fix**:
  - Refresh only affected canvas roots.
  - Batch via `fyne.Do(...)` and a single `requestAnimationFrame`-style coalesce.
  - Defer until after the SetTheme call settles (use `time.AfterFunc(0, ...)`).

### M — Onboarding endpoint selector uses display name as identifier

- **Severity**: Medium
- **Files**: `desktop/ggcode-desktop/app.go:882-899, 948-960, 982-988`
- **Description**: The v1.3.38 desktop onboarding endpoint selector matches on `DisplayName`. With MiMo (v1.3.42) introducing additional endpoint entries and vendor defaults expanding, duplicate or near-duplicate display names will silently bind the wrong endpoint.
- **Fix**: carry a hidden stable ID (vendor + endpoint key) in the select options; only render display name to the user.

### M — Session token usage persistence is non-atomic

- **Severity**: Medium
- **Files**: `desktop/ggcode-desktop/agent_bridge.go:1068-1098`
- **Description**: Usage is added in-memory, then session metadata and usage-entry are appended **separately**. A crash between writes can desync cache breakdown vs session log; concurrent updates can reorder UI vs disk.
- **Fix**:
  - Single writer goroutine per session that accepts a `usageUpdate{}` channel.
  - Or wrap both writes in `os.Rename`-based atomic replacement of a combined snapshot file.

### M — Token usage updates can race with session switches

- **Severity**: Medium
- **Files**: `desktop/ggcode-desktop/agent_bridge.go:1068-1098, 1833, 2365`
- **Description**: `recordSessionUsage()` snapshots `currentSes` under lock, but UI refresh and later session updates can observe stale totals when sessions swap quickly (e.g., open a new chat while the previous run finishes its usage tally).
- **Fix**: tag every usage update with the originating session ID; drop callbacks where `update.sessionID != current.sessionID`.

### M — `fyne.CurrentApp()` nil/window-index panic risk

- **Severity**: Medium
- **Files**: `desktop/ggcode-desktop/chat_view.go:388-401`
- **Description**: Code assumes `fyne.CurrentApp().Driver().AllWindows()[0]` always exists. During shutdown / pre-init / split-window flows, either side can be nil/empty → panic.
- **Fix**: guard `if app := fyne.CurrentApp(); app != nil { if ws := app.Driver().AllWindows(); len(ws) > 0 { ... } }`.

### M — macOS titlebar not guarded for missing standard controls

- **Severity**: Medium
- **Files**: `desktop/ggcode-desktop/titlebar_darwin.go:27-53, 55-87`
- **Description**: Code assumes `standardWindowButton:` returns usable controls. In fullscreen / borderless / custom chrome states this can be nil or in a transient state, causing the integration to crash on transition.
- **Fix**: re-check on `windowDidEnterFullScreen` / `windowWillExitFullScreen`; no-op cleanly when controls unavailable.

### M — Linux packaging likely incomplete for desktop integration

- **Severity**: Medium
- **Files**: `.goreleaser.yaml:43-70`, `.github/packaging/linux/ggcode-desktop.desktop:1-12`, `.github/packaging/linux/gg.ai.ggcode-desktop.metainfo.xml:1-24`
- **Description**: Packaging metadata files exist, but the release config primarily packages the CLI binary; explicit install of `.desktop` file / icon assets / MIME registration into `/usr/share/applications`, `/usr/share/icons/hicolor/*/apps/`, `/usr/share/mime/packages/` is not visible in the goreleaser nfpm sections.
- **Fix**: ensure deb/rpm/apk packages declare those assets in their `contents:` sections; verify with `dpkg-deb -c` on the artifact.

### M — Windows MSI lacks file association + uninstall cleanup

- **Severity**: Medium
- **Files**: `.github/packaging/windows/ggcode.wxs:3-20`, `.github/packaging/windows/ggcode-desktop.wxs:3-70`
- **Description**: MSI installs binary + shortcuts, but no file-association registration (e.g., `.ggcode` workspace file) and no explicit cleanup beyond Program Menu folder. Uninstall leaves `%LOCALAPPDATA%\GGCode` artifacts.
- **Fix**: add ProgID + association registry keys; add `RemoveFolder` for AppData paths in the uninstall sequence.

### L — Titlebar label lookup tag collision risk

- **Severity**: Low
- **Files**: `desktop/ggcode-desktop/titlebar_darwin.go:11, 64-73`
- **Description**: Label view found by fixed tag `0x67674364`. Another integration using the same tag (Fyne's custom views, third-party libs) would collide.
- **Fix**: switch to `setIdentifier:` (NSAccessibilityIdentifier) lookup, which is already what was done for the background view per the prior crash fix; apply the same pattern to the label.

### L — Onboarding persistence is partial on intermediate selections

- **Severity**: Low
- **Files**: `desktop/ggcode-desktop/app.go:908-916, 1003-1007`
- **Description**: `a.cfg.Vendor/Endpoint` and API key are mutated during selection before final submit. A crash/cancel mid-onboarding leaves config in an intermediate state.
- **Fix**: stage onboarding state in a local `onboardingDraft` struct; persist only on submit.

### L — Theme switch flicker visible on multi-window setups

- See M-themedRectangle and M-theme-refresh above; this is the user-visible symptom and was already raised in this session ("浅色主题切换到深色主题之后有两块是不是忘了跟新了").

---

## Cross-platform notes

- **macOS 12/13/14**: native titlebar appearance code is dark-only — confirmed visible regression for light-mode users.
- **Windows**: MSI installs for both amd64 and arm64 (`scripts/release/build-windows-msi.ps1`), but Defender flags and SmartScreen reputation untested at release time.
- **Linux**: deb/rpm/apk/ipk/pkg.tar.zst all built (`scripts/release/build-desktop-linux.sh`), but the desktop integration completeness varies per format and isn't smoke-tested.

---

## Recommended action items

| Priority | Item |
|----------|------|
| P0 | Predictable temp files → `os.CreateTemp()` (C-7) |
| P0 | macOS titlebar appearance follows system / theme (new M) |
| P0 | CGo `<stdlib.h>` include + main-thread guarantee (new M) |
| P1 | Onboarding endpoint stable-ID (new M) |
| P1 | Token usage persistence atomicity + session-tagged updates (2× new M) |
| P1 | Recursive goroutine chain fix (M-35) |
| P1 | LimitReader on kroki/mermaid (M-36) |
| P1 | `thinkingW` widget race (M-39) |
| P2 | themedRectangle pure-data recompute (new M) |
| P2 | Theme refresh batching / per-root (new M) |
| P2 | Linux packaging desktop integration assets (new M) |
| P2 | Windows MSI association + uninstall cleanup (new M) |
| P3 | Titlebar identifier lookup; onboarding draft state; window-index guards |
