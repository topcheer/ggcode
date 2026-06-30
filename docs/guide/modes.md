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

#### Goal-Directed Execution

In autopilot mode, the agent starts each session by defining a **Goal** via `ask_user`. The Goal is a concise 1-3 sentence definition of what "done" looks like. The agent then:

1. Works autonomously until the Goal is fully achieved
2. Anchors all work to the original task to prevent scope drift
3. Does not stop for preferences or confirmation when a reasonable default exists
4. Escalates to `ask_user` only when blocked on an external dependency
5. Ends with `GOAL_COMPLETE` when the Goal is genuinely achieved

This means you can start a session, confirm the Goal, and walk away — the agent will work through to completion.

## Session-Scoped Persistence

Permission mode is **persisted per session**, not globally:

- **New session**: uses the global `default_mode` from config (or `supervised` if unset)
- **Switching mode mid-session**: saves to session metadata (`session.PermissionMode`), **not** to global config
- **Resuming a session**: restores the mode that was active when the session was last used
- **Multiple instances**: each session tracks its own mode independently

This means switching to `bypass` in one session won't affect other sessions or future new sessions. To change the global default, edit `default_mode` in `ggcode.yaml` or use `config set default_mode=bypass`.

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
