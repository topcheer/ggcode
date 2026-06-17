# Permission Modes

ggcode uses permission modes to control how much autonomy the agent has over file writes and command execution.

## Modes

### `default`

Asks for confirmation on file writes and command execution.

```bash
ggcode
```

Best for exploring an unfamiliar codebase.

### `bypass`

Auto-approves safe operations and warns on potentially dangerous ones. Faster for trusted workflows.

```bash
ggcode --bypass
```

### `readonly`

Read-only mode — no file writes or command execution allowed. The agent can only read and search.

Best for code review and exploration.

### `yolo`

Auto-approves everything, including dangerous operations. **Use with caution.**

### `dangerous`

Same as `yolo` but adds extra warnings for destructive operations.

## Mode Indicator

The active mode is shown in the TUI status bar:

```
[default]  model: gpt-4o  |  cost: $0.012
```

## Switching Mid-Session

Switch modes without restarting using the `/mode` slash command:

```
/mode bypass
```

## Pipe Mode

When using pipe mode (`-p`), ggcode defaults to `default` mode unless `--bypass` is specified:

```bash
echo "fix the typo" | ggcode -p            # default mode (asks confirmation)
echo "fix the typo" | ggcode -p --bypass   # bypass mode (auto-approve)
```

## Recommendations

| Scenario | Mode |
|----------|------|
| Exploring unfamiliar code | `default` |
| Trusted workflow / CI | `bypass` |
| Code review | `readonly` |
| Experimental / throwaway | `yolo` |
