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

3. **Display-boundary sanitization** (`security.SanitizeTerminalForDisplay`):
   Layers 1-2 only cover `run_command` output. Tool output from every other
   source — MCP server responses, `read_file` contents, web fetches — reaches
   the user's terminal through the TUI render, streaming body, IM push, and
   desktop bridge with no filtering (sa-41). Since a hostile response can
   hijack the human review path (set the terminal title via OSC 0/2, overwrite
   the clipboard via OSC 52, open phishing links via OSC 8, clear/move the
   screen via CSI 2J/H/f/K to hide text, or switch to the alternate screen
   buffer — see ATR-2026-00259 "ANSI Escape Code Terminal Injection",
   OWASP Agentic ASI08:2026 / LLM02:2025, MITRE ATLAS AML.T0057, and the
   NVIDIA garak `ansiescape` probe), every display surface now neutralizes:

   - raw OSC/DCS/PM/APC string sequences (7-bit and 8-bit C1 encodings)
   - raw CSI sequences, including benign SGR color (display surfaces apply
     their own styling; foreign escapes can desynchronize the frame)
   - C0 control runes except newline/tab (carriage-return overwrite is a
     hide-from-review primitive)
   - literal-string escape forms encoding dangerous sequences
     (`\x1b[H\x1b[2J`, `\u001b]0;...`) become `[ansi-filtered]`; benign
     literal color codes in code examples (`\x1b[31m`) are preserved

   This layer is a pure transformation: no findings, nothing blocked
   upstream, session transcripts and agent context untouched. The
   model-facing context and the human-facing screen now agree on dangerous
   bytes.

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
