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
| `default_mode` | string | Permission mode at startup: `supervised`, `plan`, `auto`, `bypass`, `autopilot` |
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

External command-based tools:

```yaml
plugins:
  - name: my-tool
    command: /path/to/plugin
    args: ["--flag"]
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
