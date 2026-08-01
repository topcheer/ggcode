# Open Editor Tool

## Overview

The `open_editor` tool bridges the terminal-based agent loop and GUI-based code
review workflows by letting the agent open a file in the user's preferred
external editor or IDE, optionally at a specific line and column.

## Motivation

Competitors like Claude Code, Cursor, and Aider all integrate with the user's
IDE for file viewing and review. ggcode previously had no way for the agent to
launch an external editor — the only option was `run_command` with a hardcoded
editor name, which:

- Blocks the agent loop (editors like `vim` attach to the terminal)
- Requires the agent to know which editor is installed
- Doesn't support line/column jump syntax (different per editor)
- Has no fallback for unknown systems

## Design

### Editor Detection

The tool auto-detects the editor in priority order:

1. `$GGCODE_EDITOR` / `$GID_EDITOR` — ggcode-specific override
2. `$VISUAL` — standard Unix visual editor
3. `$EDITOR` — standard Unix editor
4. IDE launcher detection via `exec.LookPath`:
   - VS Code (`code`), Cursor (`cursor`), Zed (`zed`)
   - Sublime Text (`subl`)
   - JetBrains: IntelliJ (`idea`), WebStorm (`webstorm`), GoLand (`goland`), PyCharm (`pycharm`)
   - Terminal editors: Neovim (`nvim`), Vim (`vim`), Emacs (`emacs`)
5. Platform default: `open` (macOS), `xdg-open` (Linux), `start` (Windows)

### Editor-Specific Syntax

Each editor family has different line/column argument syntax:

| Editor | Syntax |
|--------|--------|
| VS Code / Cursor / Zed | `code --goto file:line:column` |
| Sublime Text | `subl file:line:column` |
| JetBrains | `idea --line N file` |
| Vim / Neovim | `vim +line file` |
| Emacs | `emacs +line file` |

### Non-Blocking Launch

The editor process is launched in detached mode so it doesn't block the agent
loop:

- **Unix**: `SysProcAttr{Setsid: true}` — new session, own process group
- **Windows**: `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS` flags

### Permission Classification

`open_editor` is **NOT** classified as a read-only tool because it launches an
external process. This means:

- **Plan mode**: denied (like all non-read-only tools)
- **Supervised mode**: requires user confirmation
- **Auto/Bypass/Autopilot**: allowed

### File Structure

```
internal/tool/
├── open_editor.go          # Tool implementation
├── open_editor_test.go     # 12 unit tests
├── detach_unix.go          # Unix: Setsid detachment
└── detach_windows.go       # Windows: DETACHED_PROCESS
```

## Future Enhancements

- Support for "reveal in file manager" (Finder/Explorer)
- Support for diff view in IDE (open two files in diff mode)
- Integration with terminal multiplexer panes (open in new tmux pane)
