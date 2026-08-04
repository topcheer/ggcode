# Terminal Environment Normalization

## Overview

When ggcode executes shell commands on behalf of the agent, the output is fed
back into the LLM context. CLI tools often produce terminal-formatted output
that is noisy and wasteful for LLM consumption: ANSI color codes, progress
bars, spinner animations, cursor movement, and variable-width wrapping.

ggcode normalizes the terminal environment for **every** command execution
to produce clean, deterministic output at the source.

## What We Inject

| Variable     | Value   | Purpose |
|-------------|---------|---------|
| `TERM`      | `dumb`  | Suppresses terminfo-based color, cursor movement, and interactive UI (progress bars, spinners) from ncurses/tput-based tools |
| `NO_COLOR`  | `1`     | The [no-color.org](https://no-color.org) standard honored by Go, Rust, Node, Python (pytest, rich), and many CLI frameworks |
| `COLUMNS`   | `120`   | Consistent wrapping width for table/list output (kubectl, terraform, pytest -v) regardless of user's terminal width |
| `CI`        | `true`  | Signals non-interactive mode to npm, cargo, gradle, gcloud, etc., suppressing interactive prompts and progress bars |

## Design

### Defense in Depth

Terminal normalization works together with post-hoc ANSI stripping
(`util.StripANSI`) as a two-layer defense:

1. **Source prevention** (this feature): Environment variables prevent color
   codes from being generated in the first place. This is strictly better than
   stripping because it avoids the CPU/memory cost of processing escape
   sequences.

2. **Safety net** (`util.StripANSI`): Any residual escape sequences that slip
   through (tools that hardcode colors regardless of environment) are stripped
   before the output enters the agent context.

### Override Semantics

The normalization overrides are appended to `os.Environ()`. In Go's
`exec.Cmd.Env`, later entries take precedence, so our values replace any
user-set `TERM`, `NO_COLOR`, `COLUMNS`, or `CI` values. All other user
environment variables (PATH, HOME, GOPATH, etc.) are preserved.

### Background Commands

The normalization is applied at command construction time, so it covers both
foreground (`run_command`) and auto-backgrounded commands identically.

## Competitive Analysis

- **Claude Code**: Sets `TERM=dumb` for all command execution
- **Aider**: Detects CI mode and adjusts output accordingly
- **Cursor**: Normalizes terminal width for consistent output
- **CI systems** (GitHub Actions, GitLab CI): Set `CI=true` universally

ggcode combines all four standard approaches for maximum coverage.
