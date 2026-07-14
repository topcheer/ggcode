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

Read-only mode — allows `read_file`, `multi_file_read`, `list_directory`, `search_files`, `glob`; denies writes and command execution. The agent can only read and search. The `lanchat` and `im` tools are always allowed (communication tools with no filesystem impact).

Best for code review, exploration, and planning before implementation.

### `auto`

Allows safe operations automatically, **denies** dangerous ones. Never prompts the user — every operation is either allowed or denied. Reduces interruptions while maintaining hard safety guardrails.

- **Dangerous commands** (`rm -rf`, `git push --force`, etc.): **Denied** (hard block, agent cannot proceed)
- **File writes outside sandbox**: **Denied**
- **File reads outside sandbox**: **Denied**
- **Normal/safe operations**: Allowed silently

### `bypass`

Auto-approves safe operations, **asks** on potentially dangerous ones. Faster for trusted workflows — the agent can proceed with most operations, and you only get prompted for genuinely risky actions.

```bash
ggcode --bypass
```

- **Extremely dangerous commands** (catastrophic, irreversible): **Ask** (you can approve or reject)
- **Dangerous commands** (not extremely dangerous): **Allowed** silently
- **File writes outside sandbox**: **Ask** (you can approve or reject per-call)
- **File reads outside sandbox**: **Allowed** silently, unless the path is sensitive (`~/.aws/credentials`, `/etc/**`, etc.) — then **Ask**
- **Normal/safe operations**: Allowed silently

### `auto` vs `bypass` — Detailed Comparison

| Scenario | `auto` | `bypass` |
|----------|--------|---------|
| Safe command (`go build`, `ls`) | Allow | Allow |
| Dangerous command (`rm -rf`, `git push --force`) | **Deny** (hard block) | **Allow** (only extremely dangerous commands trigger Ask) |
| Extremely dangerous command | **Deny** | **Ask** (can approve) |
| Write file inside workspace | Allow | Allow |
| Write file outside workspace | **Deny** | **Ask** (can approve) |
| Read file inside workspace | Allow | Allow |
| Read file outside workspace | **Deny** | Allow (unless sensitive path → **Ask**) |
| User prompts / interruptions | Never | Only for edge cases |

**Key difference**: `auto` **never prompts** — it hard-denies anything risky. `bypass` **may prompt** for edge cases (out-of-sandbox writes, extremely dangerous commands), giving you the choice to approve or reject case-by-case.

**Danger levels**: There are two tiers of command danger classification:
- `IsDangerous` — broader set (e.g. `rm -rf`, `git push --force`, `dd`). `auto` uses this to deny.
- `IsExtremelyDangerous` — narrower subset (catastrophic, irreversible). `bypass` uses this to ask; everything else is allowed.

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

Permission mode and sidebar visibility are **persisted per session** in the session JSONL meta record, not globally:

- **New session**: uses the global `default_mode` from config (or `supervised` if unset); sidebar uses global `sidebar_visible` config
- **Switching mode mid-session**: saves to `session.PermissionMode` in the JSONL file, **not** to global config
- **Ctrl+R (toggle sidebar)**: saves to `session.SidebarVisible` (`*bool`) in the JSONL file, **not** to global config
- **Resuming a session**: restores the mode and sidebar visibility that were active when the session was last used
- **Multiple instances**: each session tracks its own mode and sidebar state independently

This means switching to `bypass` in one session won't affect other sessions or future new sessions. To change the global default, edit `default_mode` in `ggcode.yaml` or use `config set default_mode=bypass`.

## Mode Indicator

The active mode is shown in the TUI status bar:

```
[supervised]  model: gpt-4o  |  cost: $0.012
```

## Switching Mid-Session

Switch modes without restarting using the `/mode` slash command, or press `Shift+Tab` in the TUI to cycle through modes in order:

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
