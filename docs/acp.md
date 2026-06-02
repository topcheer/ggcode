# ggcode ACP (Agent Client Protocol) Support

ggcode supports the Agent Client Protocol (ACP), allowing it to be used as an AI coding agent in JetBrains IDEs, Zed, and other ACP-compatible editors.

For the extracted standalone ACP client/runtime library and ggcode-side adapter boundary, see
[docs/acp-go-integration.md](acp-go-integration.md).

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

## How It Works

When you open a chat panel in your IDE, the IDE starts a `ggcode acp` process and communicates via JSON-RPC 2.0 over stdin/stdout:

```
┌─────────────────────────┐
│  IDE (JetBrains / Zed)  │
│  Chat Panel             │
└───────────┬─────────────┘
            │ stdin/stdout (JSON-RPC 2.0)
            ▼
┌─────────────────────────┐
│  ggcode acp             │
│                         │
│  Transport              │◄── initialize, session/new
│  Protocol Handler       │◄── session/prompt (user messages)
│  Agent Loop             │◄── session/cancel
│    ├── Streaming events │──► session/update (text, tool calls)
│    ├── Tool formatting  │──► "Read /tmp/test.go" (not "read_file")
│    ├── ask_user routing │──► session/request_permission (choices)
│    └── Permission checks│──► session/request_permission (approve)
│  Tool System            │
│  Session Persistence    │
└─────────────────────────┘
```

### Message Flow

1. **IDE → ggcode**: `initialize` → exchange capabilities
2. **IDE → ggcode**: `session/new` → create session with project CWD
3. **IDE → ggcode**: `session/prompt` → user sends a message
4. **ggcode → IDE**: `session/update` (agent_message_chunk) → streaming text response
5. **ggcode → IDE**: `session/update` (tool_call) → tool starts executing
6. **ggcode → IDE**: `session/update` (tool_call_update) → tool completed/failed with formatted title
7. **ggcode → IDE**: `session/request_permission` → ask for approval (dangerous tools)
8. **Repeat 3–7** until the task is done

### Tool Call Display

Tool calls appear in the IDE with human-readable titles, the same format as the TUI:

| Raw tool call | IDE displays |
|---|---|
| `read_file {"path":"/src/main.go"}` | `Read /src/main.go` |
| `edit_file {"file_path":"/src/main.go",...}` | `Edit /src/main.go` |
| `write_file {"path":"/src/new.go",...}` | `Write /src/new.go` |
| `run_command {"command":"go test ./..."}` | `go test ./...` |
| `search_files {"pattern":"TODO"}` | `Search TODO` |
| `git_diff {"cached":true}` | `Diff --cached` |
| `web_search {"query":"golang"}` | `Search golang` |
| `ask_user {"prompt":"Which file?"}` | Shows choice dialog in IDE |

Status updates show `pending` → `completed` or `failed`. Tool results are not sent to the IDE — only the formatted title and status.

### User Interaction (ask_user)

When the LLM needs to ask a question, it uses the `ask_user` tool. In ACP mode this is routed to the IDE:

- **Single/multi choice questions** → displayed as options in a permission dialog
- **Text input questions** → not supported by the permission UI, so the LLM falls back to asking in plain text within the conversation
- **User cancels** → the LLM is told the user dismissed the question and re-asks in plain text

### Permission Model

| Mode | Behavior |
|------|----------|
| `auto` (default) | Safe operations auto-approved; dangerous tools ask via IDE permission dialog |
| `bypass` / `autopilot` | All tools auto-approved (no dialogs) |
| `manual` | Every tool call requires IDE approval |

## Protocol Support

ggcode implements ACP protocol version 1 with the following capabilities:

| Feature | Status |
|---------|--------|
| `initialize` | ✅ Supported |
| `authenticate` | ✅ Supported (env_var + agent) |
| `session/new` | ✅ Supported (with CWD validation) |
| `session/prompt` | ✅ Supported (streaming) |
| `session/cancel` | ✅ Supported |
| `session/load` | ✅ Supported |
| `session/set_mode` | ✅ Supported |
| `session/update` notifications | ✅ Streaming (flat spec v1 format) |
| `session/request_permission` | ✅ Supported (tool approval + ask_user) |
| MCP servers | ✅ Dynamic connection |
| Tool calls | ✅ Full tool system with formatted display |
| ask_user | ✅ Routed through IDE permission dialog |
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

In ACP mode, ggcode uses auto permissions by default. Safe operations (read, search, list) are auto-approved; dangerous operations (write, execute) require IDE approval via `session/request_permission`.

## Multiple Instances & Workspaces

ggcode ACP fully supports running multiple instances simultaneously. Each IDE window starts its own independent `ggcode acp` process via stdio, so there is no conflict between instances.

### How It Works

```
JetBrains Window A (Project X)          JetBrains Window B (Project Y)
         │                                          │
    ggcode acp                                 ggcode acp
         │                                          │
   Session CWD: /projects/x                  Session CWD: /projects/y
   Sessions: ~/.ggcode/acp-sessions/a1b2c3/   Sessions: ~/.ggcode/acp-sessions/d4e5f6/
```

Each instance is completely independent — no shared state, no IM bridge, no daemon dependency.

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

## What ACP Does NOT Include

ACP mode is a focused IDE integration. The following features from daemon/TUI mode are intentionally excluded:

| Feature | Available in ACP? | Why |
|---------|-------------------|-----|
| TUI (terminal UI) | ❌ | IDE provides the chat UI |
| IM (instant messaging) | ❌ | IDE is the communication channel |
| WebUI | ❌ | IDE provides the interface |
| A2A (agent-to-agent) | ❌ | Single-agent per IDE window |
| Daemon bridge | ❌ | Stateless process per IDE window |

If you need IM, WebUI, or A2A features, use `ggcode` (TUI) or `ggcode daemon` mode instead.

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

### Tool calls not showing in IDE

Make sure your IDE supports ACP v1 and displays `session/update` notifications with `tool_call` / `tool_call_update` types.

## References

- [ACP Official Documentation](https://agentclientprotocol.com/)
- [ACP GitHub](https://github.com/agentclientprotocol/agent-client-protocol)
- [ACP Registry](https://github.com/agentclientprotocol/registry)
