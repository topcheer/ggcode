# Disk Space Monitor

## Overview

ggcode proactively checks available disk space on the workspace volume at the start of each agent run. When space is critically low, a concise advisory is injected into the agent's context so it can prioritize cleanup (clear caches, remove temp files, prune worktrees) before file operations fail.

## Problem

When disk space runs out, AI coding agents waste iterations on cryptic errors:
- `write_file` and `edit_file` fail with "no space left on device"
- `git commit` fails silently or with confusing lock errors
- Build commands (`go build`, `npm install`) crash mid-execution
- The agent misdiagnoses these as code bugs, retrying endlessly

No major competitor (Claude Code, Cursor, Devin, OpenHands, Aider) proactively checks disk space before working. This is especially critical for:
- Long autopilot sessions that accumulate temp files
- Harness worktrees that multiply disk usage
- CI runners with constrained ephemeral storage

## How It Works

1. **At run start**, the agent calls `syscall.Statfs` (Unix) or `GetDiskFreeSpaceEx` (Windows) on the workspace directory
2. **Two thresholds** trigger advisories:
   - **Warning** (< 2 GiB free): suggests cleanup before heavy operations
   - **Critical** (< 500 MiB free): warns that writes/builds will likely fail
3. **Caching**: results are cached for 2 minutes to avoid repeated statfs calls across rapid user turns
4. **Non-fatal**: if the stat call fails (network FS, permissions), the check is silently skipped

## Design

- **Zero LLM cost**: deterministic syscall check, no API calls
- **Fires at most once per run**: avoids repetitive context injection
- **Cross-platform**: build-tag separated implementations for Unix and Windows
- **Non-blocking**: statfs is a microsecond-level kernel call

## Files

| File | Purpose |
|------|---------|
| `internal/agent/disk_space.go` | Core state machine, threshold logic, formatting |
| `internal/agent/disk_space_unix.go` | Unix `syscall.Statfs` implementation |
| `internal/agent/disk_space_windows.go` | Windows `GetDiskFreeSpaceEx` implementation |
| `internal/agent/disk_space_test.go` | Unit tests (9 tests) |
| `internal/agent/agent.go` | Integration: struct field, init, run-start hook |

## Integration Point

The check runs at the start of `RunStreamWithContent`, after the change reconciliation capture and before the main agent loop begins. If an advisory is generated, it's injected as a `user` role message into the context manager.
