# Hooks

Hooks let you run shell commands or HTTP webhooks automatically on agent lifecycle events. Use them for security policies, auto-formatting, CI/CD triggers, audit logging, notifications, and custom automation.

## Events

| Event | Trigger | Sync/Async | Can Block |
|-------|---------|------------|-----------|
| `on_user_message` | User submits a message, before LLM call | Sync | Yes |
| `pre_tool_use` | After permission check, before tool executes | Sync | Yes |
| `post_tool_use` | After tool executes, before result returns to LLM | Sync | No |
| `on_agent_stop` | Agent loop ends (completed/cancelled/error) | Async | No |
| `on_stream_stop` | Single LLM stream response completes | Async | No |

### Execution Order

```
user message
  → on_user_message (sync, can block)
  → LLM stream
    → on_stream_stop (async)
  → [tool calls]
    → pre_tool_use (sync, can block)
    → tool.Execute()
    → post_tool_use (sync, can inject output)
  → ... (loop until no more tool calls)
  → on_agent_stop (async)
```

## Hook Types

### command (default)

Executes a local shell command via the user's default shell.

```yaml
- match: "write_file"
  command: "gofmt -w ${FILE_PATH}"
  inject_output: true  # post_tool_use only
```

### http

Sends an HTTP POST with the standardized JSON payload.

```yaml
- match: "write_file"
  type: http
  url: "https://security.example.com/scan"
  method: POST       # default: POST
  timeout: "5s"      # default: 10s
  secret: "my-hmac-key"  # HMAC-SHA256 signature in X-GGCode-Signature header
  headers:           # optional custom headers
    Authorization: "Bearer token123"
```

### Blocking Semantics

| Type | Block Method | Non-Block Error Behavior |
|------|-------------|-------------------------|
| command | exit code 2 (stderr → block reason) | Other non-zero: allow, log warning |
| http | HTTP 403 (response body → block reason) | Connection error/timeout/non-2xx: allow, log warning |

Only `on_user_message` and `pre_tool_use` can block. `post_tool_use`, `on_agent_stop`, and `on_stream_stop` always allow — block responses are ignored.

## Standard Payload

All hooks (both command and http) receive a unified JSON payload:

```json
{
  "event": "pre_tool_use",
  "session_id": "abc123",
  "workspace": "/Users/me/project",
  "timestamp": "2026-06-30T16:00:00Z",
  "tool": {
    "name": "write_file",
    "input": {"file_path": "/path/file.go", "content": "..."},
    "file_path": "/path/file.go"
  },
  "result": {
    "success": true,
    "error": "",
    "output": "File written successfully",
    "duration_ms": 12
  },
  "message": {
    "role": "user",
    "content": "fix the bug"
  },
  "stop_reason": "completed",
  "stop_error": ""
}
```

Fields populated per event:

| Event | Fields |
|-------|--------|
| `on_user_message` | `message` |
| `pre_tool_use` | `tool` |
| `post_tool_use` | `tool` + `result` |
| `on_agent_stop` | `stop_reason` + `stop_error` |
| `on_stream_stop` | `stop_reason` |

## Data Access

### command hooks

| Channel | Content |
|---------|---------|
| `${PAYLOAD}` template | Full JSON payload string |
| `GGCODE_HOOK_PAYLOAD` env var | Full JSON payload string |
| stdin | Full JSON payload |
| `GGCODE_HOOK_EVENT` env var | Event name |
| `GGCODE_TOOL_NAME` env var | Tool name (tool events) |
| `GGCODE_RAW_INPUT` env var | Raw JSON tool arguments |
| `GGCODE_TOOL_SUCCESS` | `true`/`false` (post_tool_use) |
| `GGCODE_TOOL_ERROR` | Error message (post_tool_use) |
| `GGCODE_TOOL_RESULT` | Tool output, 4KB max (post_tool_use) |
| `GGCODE_TOOL_DURATION` | Duration string (post_tool_use) |
| `${TOOL_NAME}`, `${FILE_PATH}`, etc. | Template expansion in command string |
| Unknown `$VAR` | Preserved for shell expansion |

### http hooks

| Channel | Content |
|---------|---------|
| POST body | Full JSON payload |
| `X-GGCode-Signature` header | `sha256=<hex>` HMAC if `secret` configured |
| `X-GGCode-Event` header | Event name |
| `Content-Type` header | `application/json` |

## Match Patterns

Match patterns apply to tool events (`pre_tool_use`, `post_tool_use`). For non-tool events (`on_user_message`, `on_agent_stop`, `on_stream_stop`), use `*` to match all.

### Match Modes

Hooks support two match modes, controlled by the optional `match_mode` field:

| Mode | Description | Default |
|------|-------------|---------|
| `glob` | Glob patterns with pipe-separated OR and argument matching | Yes |
| `regex` | Go regexp patterns matched against `toolName + " " + rawInput` | No |

### Glob Patterns (default)

| Pattern | Description |
|---------|-------------|
| `write_file` | Exact tool name |
| `write_*` | Glob |
| `write_file\|edit_file` | Pipe-separated OR |
| `run_command(git commit*)` | Tool name + argument substring |
| `run_command(*)` | Tool name + any arguments |
| `*` | Match everything |

### Regex Patterns

When `match_mode: regex`, the `match` field is compiled as a Go regexp and tested against the concatenation of the tool name and raw input (separated by a space). This allows complex matching like alternation, anchors, and character classes.

| Pattern | Description |
|---------|-------------|
| `write_file|edit_file` | Match either tool (regex alternation) |
| `^run_command\s+git\s+(push|force)` | Match git push/force commands |
| `delete|remove|drop` | Match any tool with these keywords in input |
| `.*` | Match everything (equivalent to `*` in glob) |

> **Note:** Invalid regex patterns will cause hook validation to fail at startup. The desktop UI includes a regex tester for interactive validation.

## Configuration

```yaml
hooks:
  on_user_message:
    - match: "*"
      type: http
      url: "https://audit.example.com/log"

  pre_tool_use:
    - match: "run_command(rm -rf *)"
      command: "echo 'blocked' >&2; exit 2"
    - match: "write_file"
      type: http
      url: "https://security.example.com/scan"
      timeout: "5s"
    - match: "^run_command\s+git\s+(push|force)"
      match_mode: regex
      command: "echo 'git push/force blocked' >&2; exit 2"

  post_tool_use:
    - match: "write_file|edit_file"
      command: "gofmt -w ${FILE_PATH}"
      inject_output: true

  on_agent_stop:
    - match: "*"
      type: http
      url: "https://ci.example.com/agent-done"

  on_stream_stop:
    - match: "*"
      command: "notify-send 'done'"
```

### Fields

| Field | Required | Description |
|-------|----------|-------------|
| `match` | Yes | Match pattern (glob or regex) |
| `match_mode` | No | `glob` (default) or `regex` |
| `type` | No | `command` (default) or `http` |
| `command` | command | Shell command string |
| `url` | http | Webhook URL |
| `method` | http | HTTP method (default: `POST`) |
| `timeout` | http | Timeout (default: `10s`) |
| `secret` | http | HMAC signing key |
| `headers` | http | Custom HTTP headers |
| `inject_output` | post | Inject stdout into tool result (default: `false`) |

### Instance Overrides

Hooks in instance config (`~/.ggcode/instances/{hash}/ggcode.yaml`) are **appended** to global hooks for the same event. Instance hooks run after global hooks.

## All Modes Supported

Hooks work in all agent modes: TUI, daemon, desktop (Wails), pipe, and ACP (JetBrains/Zed).

## Debugging

Enable debug logging:

```bash
GGCODE_DEBUG=1 ggcode
```

Hook events are logged with the `hooks` tag:
- `hooks: on_user_message hook 0: tool=* type=http url=https://...`
- `hooks: pre-tool-use hook 0: tool=run_command match=run_command(rm *) BLOCKED`
- `hooks: post-tool-use hook 0: tool=write_file success=true`
