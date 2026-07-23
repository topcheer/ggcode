# Configuration

## Config File

ggcode stores its configuration in `~/.ggcode/ggcode.yaml`. See `ggcode.example.yaml` for the full schema.

### Resolution Order

```
1. --config <path>          (explicit flag, highest priority)
2. ./ggcode.yaml             (current directory)
3. ./.ggcode/ggcode.yaml     (project-local .ggcode directory)
4. ~/.ggcode/ggcode.yaml     (user home, default location)
```

### Instance Scope

Per-workspace overrides are stored in `~/.ggcode/instances/<hash>/`. Use `scope=instance` in the config tool to save settings for a specific workspace only.

## Core Settings

| Key | Type | Description |
|-----|------|-------------|
| `vendor` | string | Provider vendor name (e.g. `openai`, `anthropic`, `google`, `deepseek`) |
| `endpoint` | string | Named endpoint key within the vendor (e.g. `default`, NOT a URL) |
| `model` | string | Model override (e.g. `gpt-4o`, `claude-sonnet-4-20250514`) |
| `api_key` | string | API key (use `${ENV_VAR}` syntax; stored in `keys.env`) |
| `default_mode` | string | Permission mode for **new** sessions: `supervised` (default), `plan`, `auto`, `bypass`, `autopilot` |
| `language` | string | Interface language: `en` or `zh-CN` |
| `max_iterations` | int | Agent loop limit per turn (0 = unlimited) |
| `allowed_dirs` | []string | Directories the agent may access |

## Vendors & Endpoints

Define multiple providers under `vendors`. Each vendor has a `protocol` and one or more named `endpoints`:

```yaml
vendors:
  openai:
    protocol: openai
    endpoints:
      default:
        base_url: https://api.openai.com/v1
        model: gpt-4o
  anthropic:
    protocol: anthropic
    endpoints:
      default:
        base_url: https://api.anthropic.com
        model: claude-sonnet-4-20250514
```

### Supported Protocols

| Protocol | Description |
|----------|-------------|
| `openai` | OpenAI-compatible API (most providers) |
| `anthropic` | Anthropic Claude native API |
| `gemini` | Google Gemini native API |
| `copilot` | GitHub Copilot (OAuth-based, no API key needed) |

See [Providers](./providers.md) for the full list of built-in vendor presets.

## API Key Security

API keys are stored in `~/.ggcode/keys.env` — **never** in the YAML file. This keeps secrets out of version control.

```bash
# keys.env (auto-managed by ggcode)
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
```

Use `${ENV_VAR}` syntax in the YAML to reference them:

```yaml
vendor: anthropic
api_key: ${ANTHROPIC_API_KEY}
```

## MCP Servers

Configure MCP (Model Context Protocol) servers for tool integration:

```yaml
mcp_servers:
  - name: filesystem
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
  - name: remote-api
    url: https://mcp.example.com/sse
    headers:
      Authorization: "Bearer ${MCP_TOKEN}"
```

See [MCP Integration](./mcp.md) for details.

## LSP Servers

Override auto-detected LSP servers or configure custom ones:

```yaml
lsp_servers:
  go:
    binary: /usr/local/bin/gopls
    args: ["-rpc.trace"]
  rust:
    binary: rust-analyzer
```

## Plugins

ggcode supports two plugin types:

### gRPC Plugins (recommended)

Run as independent subprocesses, communicate via gRPC. Zero version coupling with the host. Supports any language (Go, Python, Node.js, etc.).

```yaml
plugins:
  - name: my-grpc-tool
    type: grpc
    command: ["./bin/my-plugin"]
    env:                          # optional
      API_KEY: "secret"
```

Install via CLI:

```bash
ggcode plugin install my-grpc-tool ./bin/my-plugin --env API_KEY=secret
```

See [gRPC Plugins](grpc-plugins.md) for the full development guide and demo repos.

### Command Plugins

Wrap external shell commands as tools:

```yaml
plugins:
  - name: my-cmd-tool
    type: command
    commands:
      - name: deploy
        description: Deploy the current project
        command: ["./scripts/deploy.sh"]
```

## Tool Permissions

Per-tool permission rules:

```yaml
tool_permissions:
  run_command: ask
  write_file: allow
  git_commit: deny
```

| Value | Behavior |
|-------|----------|
| `allow` | Auto-approve |
| `ask` | Prompt for confirmation |
| `deny` | Auto-deny |

## Session-Scoped Permission Mode

The `default_mode` setting only applies to **new sessions**. When you switch
modes mid-session (via `/mode`, Tab key, or the `switch_mode` tool), the mode
is saved to the session's metadata — not to this config file. This means:

- Switching to `bypass` in one session doesn't affect other sessions
- Resuming a session restores the mode that was active when it was last used
- To change the global default for all future sessions, edit `default_mode` here
  or use `config set default_mode=bypass`

See [Permission Modes](./modes.md) for details.

## Hooks

Configure lifecycle hooks for security policies, auto-formatting, CI/CD triggers, audit logging, and custom automation:

```yaml
hooks:
  on_user_message:
    - match: "*"
      type: http
      url: "https://audit.example.com/log"

  pre_tool_use:
    - match: "run_command(rm -rf *)"
      command: "echo 'blocked' >&2; exit 2"
    - match: "^run_command\\s+git\\s+(push|force)"
      match_mode: regex
      command: "echo 'git push/force blocked' >&2; exit 2"

  post_tool_use:
    - match: "write_file|edit_file"
      command: "gofmt -w ${FILE_PATH}"
      inject_output: true
```

See [Hooks](./hooks.md) for the full guide (5 events, match patterns, command/http types, payload schema).

## Multi-Agent

### Sub-Agents

Control the behavior of sub-agents spawned via the `spawn_agent` tool:

```yaml
subagents:
  max_concurrent: 4    # Max concurrent sub-agents (0 = unlimited)
  timeout: 300s        # Timeout per sub-agent run (0 = no timeout)
  show_output: true    # Stream sub-agent output to the parent's TUI
```

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `max_concurrent` | int | 4 | Maximum number of sub-agents running simultaneously |
| `timeout` | duration | 0 (none) | Maximum duration for a sub-agent run |
| `show_output` | bool | false | Whether to stream sub-agent events to the parent's UI |

See [Multi-Agent Modes](./multi-agent-modes.md) for the full architecture guide.

### Swarm Teams

Configure persistent team-based coordination:

```yaml
swarm:
  max_teammates_per_team: 5    # Max teammates per team
  teammate_timeout: 600s       # Timeout for a teammate task (0 = no timeout)
  inbox_size: 32               # Task inbox depth per teammate
```

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `max_teammates_per_team` | int | 5 | Maximum teammates allowed in a single team |
| `teammate_timeout` | duration | 0 (none) | Maximum duration for a single teammate task |
| `inbox_size` | int | 32 | Task inbox buffer size per teammate |

## Knight

Knight is an autonomous code quality agent that runs during idle time. Disabled by default.

```yaml
knight:
  enabled: false
  trust_level: staged    # readonly | staged | auto
  daily_token_budget: 5000000   # 5M tokens default; 0 = unlimited
  idle_delay_sec: 300    # Wait time before Knight starts tasks
  quiet_hours:           # No tasks during these hours
    # - "22:00-08:00"
```

## Cron Jobs

Cron jobs support a `queue_if_busy` parameter (default: `false`):

- `queue_if_busy: false` — If the agent is busy when the job fires, the prompt is **skipped**. Use for non-critical periodic checks.
- `queue_if_busy: true` — The prompt is **queued** and runs after the current task finishes. Use for important tasks that must run.

Only recurring jobs are persisted to `~/.ggcode/cron-jobs.json` (grouped by workspace).
One-shot reminders are in-memory only and will be lost if the process exits before they fire.

## A2A (Agent-to-Agent)

Configure the A2A server for cross-instance communication:

```yaml
a2a:
  auth:
    api_key: "shared-secret"
    # oauth2:
    #   provider: "github"
    # oidc:
    #   provider: "google"
    #   client_id: "xxx"
    # mtls:
    #   cert_file: ".ggcode/certs/server.pem"
    #   key_file: ".ggcode/certs/server.key"
  lan_discovery: false
```

See [A2A Authentication](../a2a-auth.md) for the full guide.

## IM (Instant Messaging)

Configure IM adapters for remote access:

```yaml
im:
  output_mode: verbose  # verbose | quiet | summary
```

IM adapters (QQ, Telegram, Discord, Slack, DingTalk, Feishu, etc.) are configured at runtime via the TUI or daemon. See [IM Integration](./im-integration.md) for details.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `GGCODE_API_KEY` | Fallback API key (used if no key in config) |
| `GGCODE_ZAI_API_KEY` | Z.ai provider API key |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `OPENAI_API_KEY` | OpenAI API key |
| `GGCODE_DEBUG` | Enable debug logging (`1` to enable) |
| `${ENV_VAR}` | Expansion syntax used throughout YAML config |

> API keys in `keys.env` are referenced via `${VAR}` expansion in the YAML — they are never stored directly in `ggcode.yaml`.
