# ggcode ACP (Agent Client Protocol) Support

ggcode supports the Agent Client Protocol (ACP), allowing it to be used as an AI coding agent in JetBrains IDEs, Zed, and other ACP-compatible editors.

## What is ACP?

ACP is a standard protocol developed by JetBrains and Zed Industries for communication between IDEs (clients) and AI coding agents. It uses JSON-RPC 2.0 over stdio.

## Quick Start

### 1. Install ggcode

```bash
# Go install
go install github.com/topcheer/ggcode/cmd/ggcode@latest

# Or download from GitHub Releases
```

### 2. Configure ggcode

Set up your API key in `~/.ggcode/ggcode.yaml`:

```yaml
vendor: openai
endpoint: default
vendors:
  openai:
    endpoints:
      default:
        protocol: openai
        base_url: https://api.openai.com/v1
        api_key: ${OPENAI_API_KEY}
```

### 3. Use with JetBrains IDE

1. Open Settings → Tools → Agent Client Protocol
2. Click "Add Agent"
3. Set the command to: `ggcode acp`
4. Click OK

### 4. Use with Zed

Add to your Zed `settings.json`:

```json
{
  "agent": {
    "providers": [
      {
        "name": "ggcode",
        "command": "ggcode",
        "args": ["acp"]
      }
    ]
  }
}
```

## Protocol Support

ggcode implements ACP protocol version 1 with the following capabilities:

| Feature | Status |
|---------|--------|
| `initialize` | ✅ Supported |
| `authenticate` | ✅ Supported (env_var + agent) |
| `session/new` | ✅ Supported |
| `session/prompt` | ✅ Supported (streaming) |
| `session/cancel` | ✅ Supported |
| `session/load` | ✅ Supported |
| `session/set_mode` | ✅ Supported |
| `session/update` notifications | ✅ Streaming |
| `session/request_permission` | ✅ Supported |
| MCP servers | ✅ Dynamic connection |
| Tool calls | ✅ Full tool system |
| Image support | ✅ Vision models |
| Session persistence | ✅ JSON-based |

## Authentication

### Environment Variable (Recommended)

Set your API key as an environment variable:

```bash
export GGCODE_API_KEY=your-api-key
```

### Agent Auth (GitHub Device Flow)

When using ACP with an IDE, you can authenticate through ggcode's built-in GitHub Device Flow. The IDE will display a code and URL for you to complete authentication.

## Advanced Configuration

### ACP-specific settings in `ggcode.yaml`:

```yaml
acp:
  enabled: true
  log_level: info
```

### Custom System Prompt

```yaml
system_prompt: "You are a helpful coding assistant."
```

### Tool Permissions

In ACP mode, ggcode uses bypass permissions by default (auto-approves all tool calls). This is suitable for IDE integration where the IDE manages permissions.

## Troubleshooting

### "missing api key" error

Ensure your API key is configured in `ggcode.yaml` or set as an environment variable.

### No response from agent

Check stderr for error messages:
```bash
ggcode acp 2>acp.log
```

### Protocol errors

ACP uses stdout exclusively for JSON-RPC messages. If you see non-JSON output on stdout, please report it as a bug.

## Architecture

```
IDE (Client)
    │
    │ stdin/stdout (JSON-RPC 2.0)
    │
    ▼
ggcode acp
    ├── Transport Layer (stdio JSON-RPC)
    ├── Protocol Handler (initialize, session/*, authenticate)
    ├── Headless Agent Loop (reuses core agent)
    └── Tool System (file ops, search, commands, git, web, MCP)
```

## Multiple Instances & Workspaces

ggcode ACP fully supports running multiple instances simultaneously. Each IDE window starts its own independent `ggcode acp` process via stdio, so there is no conflict between instances.

### How It Works

```
JetBrains Window A (Project X)          JetBrains Window B (Project Y)
         │                                          │
    ggcode acp --config project-x.yaml         ggcode acp --config project-y.yaml
         │                                          │
   Session CWD: /projects/x                  Session CWD: /projects/y
   Sessions: ~/.ggcode/acp-sessions/a1b2c3/   Sessions: ~/.ggcode/acp-sessions/d4e5f6/
```

### Per-Workspace Configuration

**Option 1: Project-level config file (auto-discovered)**

Create `ggcode.yaml` or `.ggcode/ggcode.yaml` in your project root:

```yaml
# .ggcode/ggcode.yaml — project-specific config
vendor: anthropic
model: claude-sonnet-4-20250514
system_prompt: "You are an expert in this project's codebase."
allowed_dirs:
  - .
```

ggcode will automatically discover this config when started from the project directory.

**Option 2: Explicit config via --config flag**

Configure the IDE agent command as:

```
ggcode --config /path/to/project-config.yaml acp
```

**Option 3: Per-instance flags**

```
ggcode acp --vendor anthropic --model claude-sonnet-4-20250514
```

### Per-Workspace MCP Servers

Each `session/new` request can specify its own MCP servers:

```json
{
  "method": "session/new",
  "params": {
    "cwd": "/projects/my-app",
    "mcpServers": [
      {
        "name": "project-tools",
        "command": "node",
        "args": ["mcp-server.js"],
        "env": [{"name": "PROJECT_ROOT", "value": "/projects/my-app"}]
      }
    ]
  }
}
```

### Session Isolation

- Sessions are persisted per-workspace under `~/.ggcode/acp-sessions/<workspace-hash>/`
- Session IDs are cryptographically random (128-bit) — no collision risk
- Each process instance manages its own sessions independently

## References

- [ACP Official Documentation](https://agentclientprotocol.com/)
- [ACP GitHub](https://github.com/agentclientprotocol/agent-client-protocol)
- [ACP Registry](https://github.com/agentclientprotocol/registry)
