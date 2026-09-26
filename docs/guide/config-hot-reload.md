# Config Hot Reload

ggcode watches your config files while an interactive session is running and
applies most edits live, without a restart. This page documents exactly what
hot-reloads, what takes effect on the next turn, and what still requires a
restart, so you do not have to guess after editing `ggcode.yaml`.

## How It Works

During an interactive session, a lightweight watcher polls the config files
every 2 seconds, comparing a SHA-256 of each file's content against its own
baseline (with an mtime prefilter to keep idle cost near zero). When a change
is detected:

- The files are re-parsed. A broken YAML edit is rejected: the session keeps
  the last good config, and the parse error is logged once. Fix the syntax,
  save again, and the reload proceeds.
- The fields listed below are merged into the live session. Everything not
  listed keeps its startup snapshot value and needs a restart (conservative
  by default, so a reload can never disturb a running agent loop with
  untested structural changes).

Only the interactive session runtime starts the config watcher. One-shot,
non-interactive runs (`ggcode -p "..."`) always use the config as loaded at
process start.

## Watched Files

| File | Watched | Notes |
|------|---------|-------|
| `ggcode.yaml` (the session's active config) | Yes | Main config file |
| `~/.ggcode/vendors.yaml` | Yes | Vendor/endpoint/model definitions |
| `~/.ggcode/im.yaml` | No (deliberate) | The IM manager holds the startup snapshot; a live reconnect is out of scope. IM edits require a restart (or `/restart`). |
| `~/.ggcode/keys.env` | N/A | Auto-managed API key store; changes are picked up on provider rebuild. |
| Other section files (`mcp_deleted.yaml`, etc.) | No | Restart to apply. |

## Reloadable vs Restart-Only Matrix

### Immediate (hot, in place)

| Setting | Behavior on save |
|---------|------------------|
| `vendors.*` (endpoints, model lists, vendor definitions) | The vendor table is swapped in immediately. Name lookups, `/model` lists, and endpoint resolution see new entries at once. |

### Next-turn effective (safe on a live agent)

| Setting | Behavior on save |
|---------|------------------|
| `vendor` / `endpoint` / `model` (the active selection) | Session-scoped: a busy agent finishes its current turn on the old provider; an idle agent picks up the new selection immediately. Same channel a manual `/model` switch uses. |
| `fallback` and `fallbacks` (failover chain) | Consumed when a provider is rebuilt, so the next turn uses the new chain. |
| `knight.*` (budgets, iteration caps) | Re-applied per turn; next turn uses the new values. |
| `max_iterations` | Next turn uses the new cap. |
| `session_token_budget` | Next turn uses the new budget. |
| `tool_call_budget` | Next turn uses the new budget. |

### Restart required (startup snapshot)

Everything else in `ggcode.yaml` keeps the value it had when the session
started. Notably:

- `language`, `ui.*`, `extra_prompt`, `output_style`, `statusline.*`
- `allowed_dirs`, `protected_paths`, `sandbox.*`, `tool_permissions`
- `plugins.*`, `mcp_servers.*`, `mcp_sampling_disabled`
- `hooks`, `default_mode`, `subagents.*`, `swarm.*`, `verify.*`
- `a2a.*`, `lanchat.*`, `stream.*`, `lsp_servers.*`, `p2p.*`
- `session_timeout`, `notifications.*`, `im.*` (im.yaml is not watched)
- `probe_context`, `impersonation.*`

Use the `/restart` slash command to restart ggcode while preserving the
current session if you change any of these.

## Troubleshooting

If behavior does not match an edit you just saved:

1. **Check for a YAML syntax error.** This is the most common cause. A
   broken file prevents the reload; the session keeps the last good config
   and logs the parse error once. Fix and re-save.
2. **Confirm you edited the right file.** The watcher reacts to the session's
   active `ggcode.yaml` (which may be a workspace or instance-scoped file,
   see the resolution order in [configuration.md](configuration.md)) and
   `~/.ggcode/vendors.yaml`.
3. **Check the matrix above.** Most structural settings intentionally need a
   restart; the absence of a live effect is by design, not a failure.
4. **Non-interactive runs never reload.** `ggcode -p` reads config once at
   startup.
