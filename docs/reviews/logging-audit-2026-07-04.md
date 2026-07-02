# Logging Audit Report — 2026-07-04

## Overview

Comprehensive audit of logging across the ggcode Go codebase, covering:
- Direct terminal output bypassing the debug framework
- Log level appropriateness
- Missing logging on critical operations
- Excessive/noisy logging in hot paths

## Framework Summary

The `internal/debug` package provides categorized async file logging:
- **Tag-based routing**: Each `debug.Log(tag, ...)` call routes to a category-specific log file
- **38 categories**: agent, context, openai, anthropic, gemini, provider, IM platforms, tui, webui, daemon, etc.
- **Env var control**: `GGCODE_DEBUG=1` (all), `GGCODE_DEBUG=agent,provider` (selected), `GGCODE_DEBUG_AGENT=1` (per-category)
- **Verbose mode**: `GGCODE_DEBUG_AGENT=2` enables trace-level logging
- **Async writes**: Buffered channel + background goroutine, non-blocking
- **Auto-cleanup**: Log files removed on graceful exit; stale files from crashed processes cleaned on startup

## Issues Found and Fixed

### 1. CRITICAL: `desktop/app.go` — `fmt.Printf` bypassing debug framework (3 sites)

**Severity**: High — direct terminal output in desktop (Wails) app where stdout/stderr may not be visible.

| Line | Before | After |
|------|--------|-------|
| 1073 | `fmt.Printf("initIMRuntime panic: %v\n", r)` | `debug.Log("desktop", "initIMRuntime panic: %v", r)` |
| 1121 | `fmt.Printf("im: auto-muted IM channels...")` | `debug.Log("desktop", "im: auto-muted IM channels...")` |
| 1188 | `fmt.Printf("IM adapter start error: %v\n", err)` | `debug.Log("desktop", "IM adapter start error: %v", err)` |

### 2. HIGH: `cron/scheduler.go` — stdlib `log.Printf` instead of `debug.Log` (2 sites)

**Severity**: Medium — stdlib `log` writes to stderr unconditionally, bypassing the debug framework's env-var gating.

- Lines 303, 341: `log.Printf("[cron] failed to persist removal of broken job %s: %v", ...)`
- Fixed to `debug.Log("cron", ...)` + added success log for job removal

### 3. HIGH: `cron` package — zero `debug.Log` calls across all 3 source files

**Severity**: Medium — cron job lifecycle (create/delete/load/migrate) had no observability.

Added logging for:
- `Load()` — restored job count, corrupt store file
- `Create()` — job creation with expression
- `Delete()` — job removal, persistence failure
- `MigrateWorkspaceJobs()` — migration count, write failures

### 4. MEDIUM: `runfile` package — zero `debug.Log` calls

**Severity**: Medium — port file write/remove/stale-cleanup operations had no observability.

Added logging for:
- `Write()` — success (with session/pid/addr), write failure, rename failure, dir creation failure
- `readAtPath()` — stale file removal, legacy format removal, parse errors

### 5. MEDIUM: `session/store.go` — only 2 debug.Log calls (now 5)

**Severity**: Medium — session persistence is critical, error paths were silent.

Added logging for:
- `Save()` — success (with message count), rename failure, close failure
- `loadIndex()` — corrupt index detected (now logged before rebuild)

### 6. MEDIUM: `agentruntime/tunnel_host.go` — silent error swallowing

**Severity**: Medium — `_ = store.Save(ses)` discarded persistence errors for tunnel events.

Fixed to log errors instead of silently discarding:
```go
// Before
_ = jsonlStore.AppendTunnelEventToDisk(ses, record)
_ = store.Save(ses)

// After
if err := ...; err != nil {
    debug.Log("tunnel", "TunnelHost: failed to persist tunnel event...")
}
```

## Issues Identified but NOT Fixed (by design)

### `cmd/ggcode/daemon.go` — 79 `fmt.Fprintf(os.Stderr)` calls

**Assessment**: Acceptable. The daemon has no TUI — stderr is the primary user-facing output channel.
- Startup messages ("daemon started", workspace, tunnel URL)
- MCP OAuth flow prompts (browser URLs, device codes)
- Knight scheduler notifications
- Tunnel QR code display

These are **intentional user-facing output**, not diagnostic logs. Converting them to debug.Log would hide them from users.

### `cmd/ggcode/pipe.go` — 20 `fmt.Fprintf(os.Stderr)` calls

**Assessment**: Acceptable. Pipe mode outputs to stderr for CLI consumption — this is the designed output channel.

### `cmd/ggcode/llm_probe.go` — `fmt.Printf` calls

**Assessment**: Acceptable. The probe command is a diagnostic tool that prints results to stdout — this is its primary purpose.

### `internal/tui/model.go` — `fmt.Fprintf(os.Stdout)` for terminal title

**Assessment**: Acceptable. This sets the terminal title via OSC escape sequence — must go to stdout.

## Coverage Analysis

| Package | Files | debug.Log calls | Assessment |
|---------|-------|----------------|------------|
| agent | 11 | 86 | Excellent |
| provider | 16 | 80 | Excellent |
| im | 64 | 403 | Excellent |
| tui | 160 | 96 | Good (large package) |
| context | 2 | 25 | Excellent |
| desktop | 11 | 40 | Good (was 37, +3 fixed) |
| tunnel | 14 | 17 | Adequate |
| agentruntime | 26 | 20 | Fair (was 18, +2 fixed) |
| cmd | 22 | 30 | Good |
| lanchat | 7 | 7 | Adequate |
| webui | 7 | 6 | Adequate |
| session | 5 | 5 | Fair (was 2, +3 fixed) |
| cron | 3 | 8 | Fixed (was 0) |
| runfile | 3 | 4 | Fixed (was 0) |
| tool | 73 | 8 | Low — but tool errors are returned via results, not logged |
| safego | 1 | 0 | N/A (uses debug.Log via SetLogger callback) |

## Recommendations

1. **Consider adding a "runfile" category** to the debug framework — currently runfile logs route to the empty category (main log only). Adding it to `Categories` would enable `GGCODE_DEBUG_RUNFILE=1` filtering.

2. **Consider adding a "cron" category** — same rationale.

3. **tool package**: The low debug.Log count (8/73 files) is mostly by design — tool execution errors are returned as tool results visible to the user and persisted in session history. However, consider adding debug.Log for **unexpected** tool infrastructure errors (registry failures, timeout enforcement).

4. **session package**: Consider adding debug.Log for `AppendMessageToDisk` and `AppendMetaToDisk` failures — these are called from hot paths and failures may silently corrupt session files.
