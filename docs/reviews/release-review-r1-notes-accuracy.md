# Release Notes Accuracy Review: v1.3.69 – v1.3.74

**Reviewer:** notes-accuracy teammate  
**Date:** 2025-06-19  
**Method:** Each release note was compared against `git log vX..vY --oneline` and `git diff vX..vY --stat` plus targeted diff inspection of specific claims. Version metadata was cross-checked across npm, Python (pyproject.toml), and mobile (pubspec.yaml).

---

## Version Number Consistency

All six releases have consistent version numbers across npm (`package.json`), Python (`pyproject.toml`), and mobile (`pubspec.yaml`).

| Version | npm | Python | Mobile |
|---------|-----|--------|--------|
| v1.3.69 | 1.3.69 | 1.3.69 | 1.3.69+2026060902 |
| v1.3.70 | 1.3.70 | 1.3.70 | 1.3.70+2026061201 |
| v1.3.71 | 1.3.71 | 1.3.71 | 1.3.71+2026061709 |
| v1.3.72 | 1.3.72 | 1.3.72 | 1.3.72+2026061710 |
| v1.3.73 | 1.3.73 | 1.3.73 | 1.3.73+2026061902 |
| v1.3.74 | 1.3.74 | 1.3.74 | 1.3.74+2026061904 |

**Result: PASS** — No version mismatches found.

---

## v1.3.69

**Accuracy Rating: Accurate**

### Claims Verified

| Claim | Status | Evidence |
|-------|--------|----------|
| Restored Python default TLS verification by removing insecure SSL context | Verified | `python/ggcode_release_installer/cli.py` — removed `_build_ssl_context()` (had `check_hostname=False`, `verify_mode=ssl.CERT_NONE`), `_urlopen` now calls `urllib.request.urlopen(url)` without context |
| Added Python tests for secure download behavior | Verified | `python/tests/test_cli.py` — added `test_urlopen_uses_default_tls_verification` asserting `urlopen` called without `context=` param |
| Bumped npm, Python, and mobile version metadata | Verified | All three metadata files updated to 1.3.69 |

### Missing Items

None.

### Inaccurate Claims

None.

### Notes

Clean, focused release. All claims are fully backed by code changes. The release note scope is appropriately narrow (2 commits, 12 files changed).

---

## v1.3.70

**Accuracy Rating: Mostly Accurate**

### Claims Verified

All major claims verified against the 154-file diff:

| Claim | Status |
|-------|--------|
| Desktop message rendering fix (tool-use rounds as separate messages) | Verified — `desktop/wailskit/chat.go` (+319 lines), `desktop/ggcode-desktop-wails/frontend/src/components/ChatView.tsx` (+462 lines) |
| Tmux enhancements (/tmux actions, InferDefaultLayout, --setup) | Verified — `internal/tmux/` package (manager.go, client.go, layout_infer.go, store.go ~1065 lines new), `internal/tui/tmux.go` (653 lines) |
| Cancel confirmation in TUI (two Ctrl-C presses) | Verified — `internal/tui/update_keys.go` — added `cancelConfirmPending` state machine |
| Tunnel active-session barrier (barrier_event_id, barrier_ordinal, projection_hash) | Verified — `internal/tunnel/protocol.go`, `internal/tunnel/broker.go` |
| Desktop turn-based streaming (turn_id, message_id, seq) | Verified — `desktop/wailskit/chat.go` — `desktopTurnID`, `desktopTextSeq`, `startDesktopTurnLocked` |
| Desktop clipboard attachments (macOS AppleScript) | Verified — `desktop/ggcode-desktop-wails/app.go` — `ReadClipboardAttachments()`, `ReadClipboardImage()` |
| `buildSessionHistoryFromMessages` skips system messages | Verified |
| `chatStreamState.ts` extracted from ChatView | Verified — 151 lines new + 155-line test |
| Shared agentruntime helpers extracted to `sessions_shared.go` | Verified — `GroupWorkspaceSessions` confirmed |
| shutdown() protection with sync.Once | Verified — `shutdownOnce sync.Once` + `shutdownOnce.Do()` in chat.go |
| Homebrew cask `depends_on macos: :ventura` (symbol form) | Verified — diff shows `depends_on macos: ">= :ventura"` → `depends_on macos: :ventura` |
| .gitignore updated for `.playwright-mcp/` and `*.tsbuildinfo` | Verified |
| Desktop frontend deps updated (Vite 8, Vitest 4) | Verified — `vitest: ^4.1.8` added |

### Missing Items

1. **Desktop TeamBoard component** (`TeamBoard.tsx`, 258 lines) — A new desktop UI component for team/swarm board was added (commit `f3aa22b4 feat: add desktop image paste and team board`) but is not mentioned anywhere in the release notes. This is a significant new feature.
2. **Idle runner board updates** — Commit `3fcf8fd3 fix: emit board updates from idle runner` and `internal/swarm/` changes (idle_runner.go, manager.go, team.go ~194 lines changed) are not mentioned.
3. **UTF-8 textarea cursor offset fix** — Commit `a1ac0f90 fix: handle UTF-8 textarea cursor offsets` is not mentioned.
4. **Linux clipboard image error improvements** — Commit `e6a68054 fix: improve Linux clipboard image errors` not mentioned (minor).
5. **Debug skill inline loading** — Commit `ba76f47e fix: load debug skill inline` not mentioned (minor).
6. **Windows clipboard paste fix** — `internal/image/clipboard_windows.go` (+46 lines) and test added but not explicitly called out (the release notes mention "Desktop clipboard attachments" for macOS only).

### Inaccurate Claims

None — all stated claims are accurate. The notes are comprehensive but omit several feature changes.

### Notes

The release notes are thorough and every claim checks out. The main gap is the missing TeamBoard component, which is a notable new feature. The swarm/team board infrastructure changes are also absent.

---

## v1.3.71

**Accuracy Rating: Has Issues**

### Claims Verified

| Claim | Status |
|-------|--------|
| Mobile session title display (connect screen, chat header, session switcher) | Verified — `3d8ecc60` feat: include session Title in SessionInfoData, multiple title preservation fixes in broker/host/mobile |
| Mobile session switch rendering (cached messages, incremental replay) | Verified — `be03b659` fix: sync chat messages to snapshot, `6feee466` fix: stop clearing messages on session switch |
| Mobile connection status (LIVE badge from sessionReady, stale cleanup >6h) | Verified — `cleanupStale({Duration maxAge: const Duration(hours: 6)})` in connection_store.dart |
| App Store What's New fix (all 16 locales, abort on failure) | Verified — `d7b149ba fix: set What's New for all 16 supported locales`, `1707ea31 fix: App Store What's New failure now aborts submission` |
| Desktop session title in tunnel (SessionInfo events) | Verified — `7fe935f8` fix: send session_info before AttachOnlineBroker |
| `cleanupOldSessions()` deletes session data >7 days | Verified — `cleanupOldSessions({Duration maxAge: const Duration(days: 7)})` in workspace_cache.dart |
| Immediate connection removal on permanent room failure | Verified — `fef63bb9` relay error frame handling |

### Missing Items (Significant)

This release has ~85 commits but the release notes describe only the user-facing session title/switch improvements, omitting a **massive mobile infrastructure rewrite**:

1. **Multi-session connection architecture** — `connection_store.dart` (417 lines, brand new), `background_connection_manager.dart` (210 lines, brand new). This is the foundation for all the mobile fixes mentioned but represents a major architectural change.
2. **Multi-relay concurrent connections** — Commit `528eb8c6 feat: support multiple concurrent relay connections` — users can now connect to multiple ggcode hosts simultaneously. Not mentioned.
3. **Relay protocol v3-only** — Commits `c4866c57`, `000e45ca` — removed all v1/v2 compatibility code from both relay server and mobile. Breaking protocol change not mentioned.
4. **Flutter SDK upgrade** — Commit `977a0b13 chore: upgrade Flutter 3.38.7 → 3.44.2 + all deps` — major dependency upgrade not mentioned.
5. **iOS deployment target 17.0** — Commit `3265aaf5` — bumped from 13.0 to 17.0 because Apple rejects 13.0 for 64-bit-only apps. Significant compatibility change not mentioned.
6. **TUI auto-copy share URL to clipboard** — Commit `5ede55f5 feat: persist workspace info in relay rooms` + `internal/tui/tunnel.go` — share URL auto-copied on share start/refresh. New UX feature not mentioned.
7. **Workspace persistence in relay rooms** — Commits `7f0d9782 feat: persist workspace info in relay rooms`, extensive workspace_cache.dart rewrite (792 lines changed). Not mentioned.
8. **DB migration (drop cache_workspaces.url column)** — Commit `7efbb077 fix: DB migration v1→v2`. Schema change not mentioned.
9. **ShareDialog label change** — Commit `d7a6c286` updated iOS label from "TestFlight" to "App Store". Not mentioned.
10. **Workspace-grouped session switcher** — Commit `be18d009 feat: multi-relay connections + workspace-grouped switcher`. UI restructure not mentioned.
11. **30+ additional mobile bugfixes** — session offline detection, auth ticket handling, roomId extraction, renew_token persistence, workspace display name fixes, etc. These are all individually significant fixes grouped under "session switch rendering" but represent deep architectural work.

### Inaccurate Claims

None — every claim that is made is accurate. The issue is one of **omission**, not inaccuracy.

### Notes

The release notes are accurate in what they state, but they dramatically understate the scope of changes in v1.3.71. This release includes a ground-up rewrite of the mobile connection architecture (multi-session, multi-relay, v3-only protocol), a Flutter SDK upgrade, an iOS deployment target change, and TUI share URL auto-copy — none of which are mentioned. The notes describe the *symptoms fixed* (titles, session switching) but not the *infrastructure* that was rebuilt to achieve them. A user reading these notes would not understand that this was a major architectural release.

---

## v1.3.72

**Accuracy Rating: Mostly Accurate**

### Claims Verified

All highlight claims verified:

| Claim | Status |
|-------|--------|
| CLI `version` subcommand + global flags (--version, -v) | Verified — `cmd/ggcode/root.go` — `versionCmd` added, global `--version`/`-v` flags, exits before config loading |
| CLI help overhaul (removed hardcoded help, cobra dynamic) | Verified — `cmd/ggcode/main.go` (-47 lines, removed `SetHelpTemplate`/`SetUsageTemplate`) |
| MCP install wizard (interactive) | Verified — `cmd/ggcode/mcp_cmd.go` — `mcpInstallWizard()` with name, transport, command, env, headers prompts |
| IM config wizard (interactive) | Verified — `cmd/ggcode/im_cmd.go` — `imConfigAddWizard()` with adapter name, platform picker, config |
| Interactive installer (install.sh, install.ps1, perUser default) | Verified — `scripts/install/install.sh` (231 lines), `scripts/install/install.ps1` (191 lines) |
| Desktop Windows ARM64 MSI | Verified — `scripts/release/build-desktop-windows.ps1` (+113 lines), arm64 WXS files |
| Website redesign | Verified — `docs/site/index.html` (606 lines changed), `script.js`, `styles.css` (1010 lines changed) |
| README restructured to 91 lines, 16 guide docs | Verified — README.md is exactly 91 lines at v1.3.72 tag; 16 files under `docs/guide/` |
| Fixed install.sh archive name (GoReleaser omits version) | Verified — `d180977f` |
| Fixed install.sh hanging after curl pipe | Verified — `a7815e14` added `exit 0` |
| Fixed install scripts calling `ggcode version` on old builds | Verified — `a47208aa` |
| FindOtherInstalls(), DetectDualScopeWindows() | Verified — `internal/update/detect.go` |
| Privilege-aware update (UAC elevation) | Verified — `internal/update/elevate_windows.go` (+71 lines) |
| Write permission check before download | Verified — `011fc710` |
| perUser MSI different UpgradeCode | Verified — `ggcode-user.wxs`, `ggcode-desktop-user.wxs` |
| npm `requestedVersion === "latest"` stale binary bug | Verified — `b40e48d4` |
| Railway deployment fixes (orphan branch, docs/site/) | Verified — multiple commits |
| `scripts/install/**` in site-release trigger paths | Verified — `.github/workflows/site-release.yml` |
| railway.json generation | Verified — `dd97fcca` |
| i18n HTML tag rendering fix | Verified — `28270f5f` |

### Missing Items

1. **Stopping 1.6 GB binary commits** — Commit `4aa290fc fix: stop committing ~1.6GB binaries to git per release`. This is a major repository hygiene fix (repo size reduction) that is completely absent from the release notes.
2. **Website download links point to GitHub Releases** — Commit `b19f31c7` — changed download links from hosted binaries to GitHub Releases. Not mentioned.
3. **Windows Ctrl+V clipboard image paste fix** — Commit `d5b91e6e fix: Windows Ctrl+V clipboard image paste silently does nothing` — the release notes mention "npm Windows wrapper" but don't explicitly call out this clipboard fix.
4. **Multiple iterative winget YAML fixes** — The release notes mention winget perUser migration and arm64 URL, but ~12 iterative commits fixing winget YAML parser issues, installer count handling, and wingetcreate CLI flags are not individually documented. The summary approach is reasonable.

### Inaccurate Claims

None.

### Notes

Very comprehensive release notes. The biggest omission is the 1.6 GB binary cleanup, which materially affects repo size and clone performance. The Windows clipboard paste fix is also notable. All claims that are made are accurate.

---

## v1.3.73

**Accuracy Rating: Has Issues**

### Claims Verified

| Claim | Status |
|-------|--------|
| iTerm2 terminal tool exists | Verified — `internal/tool/iterm2.go` (211 lines), `iterm2_darwin.go` (837 lines), `iterm2_other.go` (98 lines) |
| Kitty terminal tool exists | Verified — `internal/tool/kitty.go` (207 lines), `kitty_impl.go` (662 lines), `kitty_test.go` (315 lines) |
| Warp terminal tool exists | Verified — `internal/tool/warp.go` (179 lines), `warp_darwin.go` (172 lines), `warp_other.go` (24 lines) |
| Ghostty works on Linux via GIO DBus IPC | Verified — `internal/tool/ghostty_linux.go` (277 lines, `//go:build linux`) |
| Winget duplicate PR prevention | Verified — `.github/workflows/release.yml` — `gh pr list --search` check before submission |
| `start_command` detach parameter | Verified — `internal/tool/command_job_tools.go` — `Detach bool`, timeout ignored when `detach=true` |
| Ctrl+G conflict fix | Verified — `internal/tui/update_keys.go` — removed ctrl+g image paste binding |
| Ctrl+Shift+V replaces Ctrl+I | Verified — `internal/tui/update_keys.go` — `case "ctrl+v", "ctrl+shift+v":` |
| A2A async discovery | Verified — `5200b957 fix: make A2A instance discovery fully async` |
| A2A instances injected into system prompt | Verified — `fe019d5f feat: inject discovered A2A instances into system prompt` |
| Auto-migrate a2a.api_key to auth.api_key | Verified — `6fd080cb` |
| Removed legacy A2A.APIKey field | Verified — `00e1a23f` |

### Inaccurate Claims

1. **iTerm2 "16 actions"** — The actual code (`iterm2.go` switch statement) has **19 action cases**: `list`, `split`, `new_tab`, `new_window`, `focus`, `close`, `select_tab`, `input`, `send_key`, `resize`, `get_text`, `set_title`, `profile`, `badge`, `broadcast`, `mark`, `clear`, `action`, `reload_config`. Even excluding the introspection `list` action, there are **18 actions**, not 16. The count is understated by 2.

2. **Kitty "17 actions"** — The actual code (`kitty.go` switch statement) has **16 action cases**: `list`, `split`, `new_tab`, `new_window`, `focus`, `close`, `close_tab`, `select_tab`, `input`, `send_key`, `resize`, `get_text`, `zoom`, `set_tab_title`, `action`, `reload_config`. Excluding `list`, there are **15 actions**, not 17. The count is overstated by 2.

3. **Ghostty framing as enhancement** — The release notes say "Ghostty tool now works on Linux via GIO DBus IPC, in addition to existing macOS AppleScript support." This implies the macOS version pre-existed. In reality, the Ghostty tool was **first created** in this same release (commit `1adc1106 feat: add ghostty terminal pane management tool`) — both the macOS and Linux backends are new. The phrasing is misleading.

### Missing Items

1. **Ghostty was brand new** — As noted above, the entire Ghostty tool (macOS + Linux) was created in this release, not just "Linux support". The release notes should have listed Ghostty as a new terminal tool alongside iTerm2 and Kitty.
2. **iOS upload idempotency check** — Commit `bfb398ab` added iOS upload idempotency check to mobile-release.yml. Not mentioned.
3. **Ghostty iterative bugfixes** — Six separate commits (`01664c48`, `207400c5`, `f1b14faa`, `15755607`, `f77816e4`, `e316d923`) fixing Ghostty split resize, new_tab AppleScript syntax, select_tab ordinal specifiers, and terminal_id handling. The release notes summarize some of these but not all.
4. **Cross-platform CI check for build tags** — Commit `8c28d5da fix: add darwin build tag to ghostty_test.go + cross-platform CI check`. Not mentioned.

### Notes

The release notes are well-structured and most descriptions of the new terminal tools are detailed and helpful. However, two specific action counts are wrong (iTerm2 understated by 2, Kitty overstated by 2), and the Ghostty tool is incorrectly framed as an enhancement rather than a brand-new tool.

---

## v1.3.74

**Accuracy Rating: Mostly Accurate**

### Claims Verified

| Claim | Status |
|-------|--------|
| Extpane for sub-agent/teammate tabs (tmux, Kitty, iTerm2) | Verified — `internal/tui/extpane/` package (8 files, 1106 lines): `manager.go`, `format.go`, `tmux.go`, `kitty.go`, `iterm2.go`, `iterm2_other.go` |
| Live output via tail -f, auto-close on finish | Verified — manager.go uses log file + tail -f, closes tab on agent done |
| Three auto-detected backends (tmux when $TMUX set, Kitty via KITTY_WINDOW_ID, iTerm2 via TERM_PROGRAM) | Verified |
| maxPanes=10 hard cap | Verified — `const maxPanes = 10` in manager.go |
| Permanent blocklist after first failure | Verified — `failed map[string]bool` — "permanently failed — never retry CreateTab", set on first failure |
| Self-window ID capture prevents closing ggcode's own tab | Verified — `selfWindowID string` in tmux.go, captured via tmux, CloseTab refuses to kill selfWindowID |
| Kitty backend uses --type=tab | Verified — `iterm2.go` and `kitty.go` both use `--type=tab` |
| tmux suppresses after-new-window hook | Verified — tmux.go saves, unsets (`set-hook -g -u after-new-window`), and restores `after-new-window` hook |
| Sub-agent completion shown as system message only when main agent busy | Verified — `internal/tui/update_subagent.go` — changed from `queuePendingSubmissionHidden` to system message when agent busy |
| Fixed Kitty --type=window → --type=tab | Verified — `3025aaca` |
| Fixed tmux rename prompt hooks | Verified — `c06f9025`, `8fe87199`, `98548963`, `591f03f5` |
| Prevented extpane tab explosion and self-closure | Verified — `99366242` |

### Missing Items

None of significance. Mobile build number bump (commit `ebeda51d`) is trivial boilerplate.

### Inaccurate Claims

None.

### Notes

Clean, accurate release notes that fully describe the extpane feature and its fixes. All technical details (maxPanes, blocklist, self-window capture, hook suppression) are precise and verified. This is the most accurate release in the batch.

---

## Summary

| Version | Rating | Key Issues |
|---------|--------|------------|
| v1.3.69 | **Accurate** | No issues found |
| v1.3.70 | **Mostly Accurate** | Missing: TeamBoard component, swarm board updates, several minor fixes |
| v1.3.71 | **Has Issues** | Major omissions: multi-session architecture, multi-relay, v3-only protocol, Flutter upgrade, iOS 17.0, TUI share URL auto-copy — ~85 commits summarized as 5 highlight items |
| v1.3.72 | **Mostly Accurate** | Missing: 1.6 GB binary cleanup, Windows clipboard paste fix |
| v1.3.73 | **Has Issues** | Inaccurate action counts (iTerm2: 16 claimed vs 18 actual; Kitty: 17 claimed vs 15 actual). Ghostty misframed as enhancement rather than new tool. |
| v1.3.74 | **Mostly Accurate** | No issues found; all claims verified |

### Overall Assessment

- **Version numbers:** Fully consistent across all releases.
- **No fabricated claims:** Every feature described in any release note has corresponding code. Zero hallucinated or invented features.
- **Primary weakness is omission:** The most significant issue is v1.3.71's notes omitting a major mobile architecture rewrite, multi-relay support, protocol changes, and Flutter upgrade.
- **Two factual count errors:** v1.3.73's iTerm2 and Kitty action counts are both off by 2 in opposite directions.
- **Best release notes:** v1.3.74 (precise, complete) and v1.3.69 (appropriately scoped).
- **Needs revision:** v1.3.71 (major omissions) and v1.3.73 (factual count errors).
