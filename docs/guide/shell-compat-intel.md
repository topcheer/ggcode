# Shell Compatibility Intelligence

## Overview

AI coding agents frequently generate shell commands trained on Linux (GNU coreutils), which fail on macOS/BSD due to flag and behavior differences. Shell Compatibility Intelligence detects these incompatibilities automatically and suggests the correct cross-platform alternative.

## Problem

When an agent runs `sed -i 's/foo/bar/' file.txt` on macOS, BSD sed requires a backup suffix argument and produces a cryptic error like `extra characters at the end of command`. Without guidance, the agent wastes multiple iterations debugging instead of switching to `sed -i '' 's/foo/bar/' file.txt`.

Common incompatibilities:

| Command | GNU (Linux) | BSD (macOS) |
|---------|------------|-------------|
| In-place edit | `sed -i 's/x/y/' f` | `sed -i '' 's/x/y/' f` |
| Canonical path | `readlink -f path` | `realpath path` |
| PCRE grep | `grep -P 'pattern'` | `grep -E 'pattern'` or `rg` |
| Date math | `date -d 'yesterday'` | `date -v-1d` |
| File stat | `stat -c '%s' f` | `stat -f '%z' f` |
| Find format | `find . -printf '%p\n'` | `find . -exec echo {} \;` |
| Drop last N | `head -n -N f` | `sed '$d' f` |
| Timeout | `timeout 5 cmd` | `gtimeout` or perl alarm |
| Version sort | `sort -V` | `sort -t. -k1,1n` |
| Dir depth | `du --max-depth=1` | `du -d 1` |
| No empty xargs | `xargs -r` | guard with `[ -s ]` |

## How It Works

The detection runs **zero-LLM-cost** pattern matching on both:
1. **Proactive**: The command string itself (detects GNU-only flags before errors occur)
2. **Reactive**: The combined stdout/stderr output (detects from BSD error messages)

When a known incompatibility pattern is detected, a `[Shell Compat]` diagnostic is appended to the command result with the correct alternative.

## Integration Points

- `run_command` tool: Fires on command failure (exit code != 0)
- Background command jobs: Fires when a background job fails

## Platform Behavior

- **macOS/BSD**: Full detection active for all patterns
- **Linux**: Detection is skipped (all GNU commands are native)

## Files

- `internal/tool/shell_compat_intel.go` -- Detection logic and pattern table
- `internal/tool/shell_compat_intel_test.go` -- Test suite (16 tests)
- `internal/tool/run_command.go` -- Integration into run_command error path
- `internal/tool/command_jobs.go` -- Integration into background job error path
