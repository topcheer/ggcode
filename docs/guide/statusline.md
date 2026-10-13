# External Status Line (statusline)

ggcode supports a user-scripted status line, mirroring the 2026 Claude Code
`statusLine` convention: a shell command receives a JSON session snapshot on
stdin, and its first stdout line is rendered as a persistent bar above the
composer.

## Configuration

```yaml
# ~/.ggcode/ggcode.yaml
statusline:
  command: "~/.ggcode/statusline.sh"   # empty (default) disables the feature
  timeout_ms: 2000                      # optional, default 2000
  osc_7501: false                       # optional, emit OSC 7501 state records
```

## OSC 7501 Program Status Protocol

Set `statusline.osc_7501: true` (independent of `command`) and ggcode emits
[OSC 7501](https://mitchellh.com/writing/program-status-osc7501) escape records
so terminal multiplexers can surface agent state — e.g. tmux 3.5.1+
`pane-border-status`, WezTerm, or agent-inbox tooling:

| Record | Boundary |
| --- | --- |
| `state=working` | run submitted, or approval/diff/question answered (run resumes) |
| `state=blocked:kind=permission` | tool approval dialog shown |
| `state=blocked:kind=permission` | diff confirmation dialog shown |
| `state=blocked:kind=question` | ask_user questionnaire shown |
| `state=done` | turn finished successfully |
| `state=error` | turn finished with a failed run |
| `state=idle` | turn canceled, or TUI startup |

Record shape: `ESC ] 7501 ; state=<s>[:kind=<k>]:app=ggcode[:msg=<base64>] ST`.
`msg` (optional) is a short human-readable hint, base64-encoded and truncated
to 120 runes. Records are only written when stdout is a TTY, so piping output
to a file stays clean.

- The command runs through `sh -c` (Linux/macOS) or `cmd /c` (Windows).
- **stdin**: one JSON object (schema below).
- **stdout**: the first line becomes the status line; the rest is ignored.
- Each invocation is capped at `timeout_ms` (default 2s). A timeout or
  non-zero exit keeps the last good output; nothing is rendered until the
  first successful refresh.

## Refresh timing

Refreshes fire at run boundaries, never per render frame: when an agent run
starts and when it completes (including cancel). While an invocation is in
flight, further requests are coalesced — at most one follow-up runs after the
in-flight one completes. In practice this means at most one process spawn per
turn boundary.

## stdin JSON schema

Core fields follow the Claude Code statusline JSON, so many existing community
scripts work unmodified; `context` and `cost` are ggcode extensions.

```json
{
  "hook_event_name": "statusline",
  "session_id": "a1b2c3d4-...",
  "version": "ggcode",
  "model": { "id": "glm-4.7", "display_name": "glm-4.7" },
  "workspace": { "current_dir": "/repo", "project_dir": "/repo" },
  "context": {
    "used_tokens": 52300,
    "total_tokens": 262144,
    "percent_used": 19.9
  },
  "cost": { "session_usd": 0.42 }
}
```

## Example

```bash
#!/bin/sh
# ~/.ggcode/statusline.sh — model, branch, context usage
input=$(cat)
model=$(printf '%s' "$input" | /usr/bin/python3 -c 'import json,sys;d=json.load(sys.stdin);print(d["model"]["display_name"])')
pct=$(printf '%s' "$input" | /usr/bin/python3 -c 'import json,sys;d=json.load(sys.stdin);print(f"{d[\"context\"][\"percent_used\"]:.0f}%")')
branch=$(git -C "$PWD" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "-")
printf '%s | %s | ctx %s' "$model" "$branch" "$pct"
```

Then:

```yaml
statusline:
  command: "~/.ggcode/statusline.sh"
```

## Notes

- The rendered line is truncated to the terminal width.
- The feature is TUI-only; non-interactive pipe runs are unaffected.
- When `statusline.command` is unset, ggcode renders its built-in status UI
  exactly as before (zero behavioral change by default).
