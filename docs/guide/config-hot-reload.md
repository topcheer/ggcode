# Configuration Hot Reload

ggcode watches its config files while a session runs and applies most
provider-related edits without a restart. This page documents exactly which
settings hot-reload and which require a restart.

## How it works

- Three watchers poll files for content changes (sha256, every 2 seconds):
  - `ggcode.yaml` + `vendors.yaml` (`internal/agentruntime/config_hotreload.go`)
  - `mcp_servers.yaml` (`internal/agentruntime/mcp_hotreload.go`)
  - skill and custom command directories (`internal/agentruntime/skill_hotreload.go`)
- The watchers start in every entry point (interactive TUI, pipe mode, and
  the daemon) via `InteractiveRuntimeCore.StartBackgroundServices`.
- A broken YAML edit keeps the last good config (the change is logged and
  skipped); fix the file and the next poll applies it.
- Config hot reload is deliberately field-conservative: settings not listed
  below keep the startup snapshot value until you restart
  (`HotReloadFieldPolicy` in `config_hotreload.go`).

## Hot-reloaded without restart

Edits to `ggcode.yaml` (or `vendors.yaml` for vendor definitions) apply
within ~2 seconds:

| Setting | YAML key | Effective |
|---------|----------|-----------|
| Vendor/endpoint/model definitions and API keys | `vendors` | immediately (lookups, `/model` lists) |
| Single fallback | `fallback` | when the provider is (re)built |
| Fallback chain | `fallbacks` | when the provider is (re)built |
| Knight budgets and caps | `knight` | next turn |
| Max iterations | `max_iterations` | next turn |
| Session token budget | `session_token_budget` | next turn |
| Tool call budget | `tool_call_budget` | next turn |
| Session timeout | `session_timeout` | next turn |

Notes:

- The session's active `vendor` / `endpoint` / `model` selection is
  session-scoped: file edits never stomp a running session's provider
  choice. Switch with `/model`, or restart / start a new session to pick up
  a changed selection. Provider rebuilds triggered by `/model` or API key
  changes pick up the refreshed vendor definitions.
- `vendors.yaml` is watched in addition to `ggcode.yaml`, so endpoint and
  model list edits made by another ggcode instance propagate live.

## MCP servers (separate watcher)

`mcp_servers.yaml` hot-reloads: servers are connected or disconnected live.
Watched locations:

- Global: `~/.ggcode/mcp_servers.yaml`
- Workspace: `./mcp_servers.yaml` and `./.ggcode/mcp_servers.yaml`
  (workspace scope is used when the working directory has its own
  `ggcode.yaml`)

## Skills and custom slash commands (separate watcher)

Skill and command directories hot-reload: adding, editing, or removing
skill/command markdown files is picked up without a restart.

## Requires restart

Everything else in `ggcode.yaml` keeps its startup snapshot value,
including: `language`, `ui`, `im` (and `im.yaml` is deliberately not
watched, because the IM manager holds the startup snapshot and a live
reconnect is out of scope), `extra_prompt`, `allowed_dirs`,
`tool_permissions`, `plugins`, `hooks`, `default_mode`, `subagents`,
`impersonation`, `swarm`, `verify`, `a2a`, `lanchat`, `stream`,
`lsp_servers`, `probe_context`, `p2p`, `output_style`, `statusline`,
`notifications`, `protected_paths`, and `sandbox`.

## Sync guard

`internal/agentruntime/config_hotreload_test.go` contains
`TestConfigHotReloadDocListsRefreshedFields`, which asserts that every key
listed in the hot-reload table above still appears in this document. If you
change the refresh set in `applyFreshConfig`, update both the code and this
page in the same commit.
