# Configuration

## Config File

ggcode stores its configuration in `~/.ggcode/ggcode.yaml`. See `ggcode.example.yaml` for the full schema.

### Resolution Order

```
1. --config <path>          (CLI flag — highest priority)
2. ./ggcode.yaml             (current directory)
3. ./.ggcode/ggcode.yaml     (project-local)
4. ~/.ggcode/ggcode.yaml     (global — lowest priority)
```

## Key Settings

```yaml
# ~/.ggcode/ggcode.yaml
vendor: anthropic
endpoint: https://api.anthropic.com
model: claude-sonnet-4-20250514
language: en
default_mode: auto
max_iterations: 0          # 0 = unlimited
allowed_dirs:
  - /home/user/projects
```

| Setting | Description |
|---------|-------------|
| `vendor` | Provider vendor name (e.g. `anthropic`, `openai`, `google`, `zai`, `deepseek`, `openrouter`, `groq`, `mistral`, `moonshot`, `github-copilot`) |
| `endpoint` | Named endpoint within a vendor |
| `model` | Active model identifier |
| `language` | Response language: `en` or `zh` |
| `default_mode` | Permission mode at startup: `supervised`, `plan`, `auto`, `bypass`, `autopilot` |
| `max_iterations` | Agent loop limit per user turn (0 = unlimited) |
| `allowed_dirs` | Directories the agent may access |

### Vendor / Endpoint Architecture

Each vendor has named endpoints, each with a protocol adapter:

```yaml
vendors:
  anthropic:
    endpoints:
      default:
        protocol: anthropic
        base_url: https://api.anthropic.com
        models:
          - claude-sonnet-4-20250514
          - claude-opus-4-20250514
  openai:
    endpoints:
      default:
        protocol: openai
        base_url: https://api.openai.com/v1
```

Supported protocols: `openai`, `anthropic`, `gemini`, `copilot`.

> **Note:** Legacy `provider`/`providers` config keys are rejected with an error at load time. Use `vendor`/`endpoint`/`vendors` instead.

## API Key Storage

API keys are stored separately in `~/.ggcode/keys.env`, **never** in the YAML config file. Use `${ENV_VAR}` syntax for expansion:

```bash
# ~/.ggcode/keys.env
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
```

```yaml
# Referenced in YAML via expansion
vendors:
  anthropic:
    api_key: ${ANTHROPIC_API_KEY}
```

## IM Adapters

Instant messaging adapters are configured under the `im` section:

```yaml
im:
  output_mode: summary       # verbose | quiet | summary
  adapters:
    slack:
      enabled: true
      token: ${SLACK_TOKEN}
    discord:
      enabled: true
      token: ${DISCORD_TOKEN}
```

Supported adapters: QQ, Telegram, Discord, Slack, DingTalk, Feishu.

## MCP Servers

Model Context Protocol servers are configured under `mcp_servers`:

```yaml
mcp_servers:
  filesystem:
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
  playwright:
    command: npx
    args: ["-y", "@modelcontextprotocol/server-playwright"]
  remote-server:
    url: https://api.example.com/mcp
    headers:
      Authorization: "Bearer ${REMOTE_TOKEN}"
```

## LSP Servers

Language Server Protocol (LSP) servers provide the agent with code intelligence (go-to-definition, find references, hover info, diagnostics). Servers are **auto-detected** from PATH and workspace files — no configuration needed for standard setups.

Override the auto-detected binary or add custom arguments via `lsp_servers`:

```yaml
lsp_servers:
  go:
    binary: /usr/local/bin/gopls  # custom binary path
    args: ["-vv"]                 # additional CLI arguments
  rust:
    binary: ~/.cargo/bin/rust-analyzer
  typescript:
    binary: typescript-language-server
    args: ["--stdio"]
```

Supported language IDs: `go`, `rust`, `clang`, `lua`, `swift`, `terraform`, `yaml`, `json`, `dockerfile`, `shell`, `zig`, `java`, `typescript`, `python`, `csharp`.

In the **desktop app**, navigate to Settings > Integrations > Language Servers to view detection status and install missing servers with one click. Install scope priority: **user** (home directory) > **global** (system-wide) > **project** (inside workspace).

## Tool Permissions

Control tool access via `tool_permissions` with per-tool rules:

```yaml
tool_permissions:
  allow:
    - read_file
    - write_file
    - run_command
  ask:
    - git_commit
    - git_push
  deny:
    - enter_worktree
```

## Hooks

Run automation before or after commands via the `hooks` section:

```yaml
hooks:
  pre_command:
    - echo "Starting: $GGCODE_COMMAND"
  post_command:
    - echo "Finished: $GGCODE_COMMAND"
```

## A2A Authentication

Agent-to-Agent server authentication supports multiple schemes simultaneously:

```yaml
a2a:
  host: 0.0.0.0              # 0.0.0.0 when auth configured, 127.0.0.1 without
  auth:
    api_key: "shared-secret"           # simplest
    api_keys:                           # additional keys (any match authenticates)
      - "secondary-key"
      - "${A2A_EXTRA_KEY}"
    oauth2:
      provider: github                  # or custom issuer_url + client_id/secret
      flow: pkce                        # pkce | device
    oidc:
      provider: google
      client_id: "xxx"
    mtls:
      cert_file: ".ggcode/certs/server.pem"
      key_file: ".ggcode/certs/server.key"
      ca_file: ".ggcode/certs/ca.pem"
    allow_unauthenticated: false        # default false; only localhost allowed without auth
  lan_discovery: false                  # mDNS broadcast for LAN peer discovery
```

Instance-level override: `.ggcode/a2a.yaml` in the workspace root.

See [`docs/a2a-auth.md`](../a2a-auth.md) for the full authentication guide.

## Instance Scope

Per-workspace overrides are stored under `~/.ggcode/instances/<hash>/`. This allows different vendor/model/settings per project without touching global config.

Settings in instance scope take precedence over global config.

## Config CLI

Manage settings from the command line:

```bash
# Read a single value
ggcode config get vendor

# Set a value
ggcode config set model claude-sonnet-4-20250514

# List all settings
ggcode config list
```

## Environment Variables

| Variable | Effect |
|----------|--------|
| `HOME` | Base directory for config path resolution |
| `GGCODE_API_KEY` | Overrides the API key from `keys.env` |
| `GGCODE_ZAI_API_KEY` | ZAI provider API key |
| `${ENV_VAR}` | Expansion syntax used throughout YAML config |

> API keys in `keys.env` are referenced via `${VAR}` expansion in the YAML — they are never stored directly in `ggcode.yaml`.
