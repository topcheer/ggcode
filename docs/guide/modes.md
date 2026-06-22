# Permission Modes

ggcode uses permission modes to control how much autonomy the agent has over file writes and command execution.

## Modes

### `supervised`

Default mode. Respects per-tool permission rules, asks for confirmation on unspecified tools.

```bash
ggcode
```

Best for exploring an unfamiliar codebase or working with sensitive code.

### `plan`

Read-only mode — allows `read_file`, `multi_file_read`, `list_directory`, `search_files`, `glob`; denies writes and command execution. The agent can only read and search.

Best for code review, exploration, and planning before implementation.

### `auto`

Allows safe operations automatically, denies dangerous ones. Reduces interruptions while maintaining safety guardrails.

### `bypass`

Auto-approves safe operations and warns on potentially dangerous ones. Faster for trusted workflows.

```bash
ggcode --bypass
```

### `autopilot`

Bypass permissions plus automatically continues when the model asks for input. Escalates external blockers to `ask_user`. Enables fully autonomous workflows.

```bash
ggcode --bypass  # then switch to autopilot via /mode
```

## Mode Indicator

The active mode is shown in the TUI status bar:

```
[supervised]  model: gpt-4o  |  cost: $0.012
```

## Switching Mid-Session

Switch modes without restarting using the `/mode` slash command:

```
/mode supervised
/mode plan
/mode auto
/mode bypass
/mode autopilot
```

## Pipe Mode

When using pipe mode (`-p`), ggcode defaults to `supervised` mode unless `--bypass` is specified:

```bash
echo "fix the typo" | ggcode -p            # supervised mode (asks confirmation)
echo "fix the typo" | ggcode -p --bypass   # bypass mode (auto-approve)
```

## Recommendations

| Scenario | Mode |
|----------|------|
| Exploring unfamiliar code | `supervised` |
| Code review / planning | `plan` |
| Trusted workflow with guardrails | `auto` |
| Fast trusted workflow | `bypass` |
| Fully autonomous | `autopilot` |
