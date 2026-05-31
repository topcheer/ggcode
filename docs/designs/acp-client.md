# ACP Client: External Agents as Built-in Tools

> Status: Draft
> Author: ggcode
> Date: 2025-06-14

## 1. Overview

**Goal**: Scan for installed ACP-compatible CLI tools at startup, discover their capabilities via the ACP protocol, and register each one as a built-in tool. The LLM can then delegate tasks to these agents (e.g., Copilot, Droid) via a single `delegate` tool, or the user can explicitly request "让 copilot 做 xxx".

### Analogy

```
MCP  = Agent 调外部工具  (ggcode → MCP Server)
ACP  = 工具调外部 Agent  (ggcode → ACP Agent)
A2A  = Agent 调 Agent    (ggcode → 另一个 ggcode instance)
```

This design adds the **ACP Client** capability — ggcode acts as an ACP **Host**, spawning external agent processes and routing user prompts to them.

## 2. Background: Current ACP Implementation

ggcode already has a complete **ACP Server** implementation (`internal/acp/`):

| Component | File | Role |
|-----------|------|------|
| Transport | `transport.go` | JSON-RPC 2.0 over stdio, bidirectional `SendRequest`/`ReadAnyMessage` |
| Types | `types.go` | All ACP protocol types (893 lines) |
| Handler | `handler.go` | Server-side method routing (`initialize`, `session/new`, `session/prompt`, etc.) |
| AgentLoop | `agent_loop.go` | Headless agent loop converting provider stream events to ACP notifications |
| Session | `session.go` | Session persistence (JSON) with conversation history |
| Auth | `auth.go` | GitHub Device Flow, env var validation |

**Key insight**: The Transport layer is already bidirectional — `SendRequest()` sends requests and waits for responses, `ReadAnyMessage()` handles both incoming requests and responses. This same Transport can be reused as the Client side.

## 3. Market Survey: ACP Agent Availability

| CLI | Version | ACP Mode | session/resume | Auth |
|-----|---------|----------|----------------|------|
| **Copilot CLI** (`gh copilot --acp`) | 1.0.56 | Yes | No (only `list`) | GitHub OAuth (built-in) |
| **Droid** (`droid --acp`) | 0.137.1 | Yes | Yes | GitHub OAuth + env vars |
| **OpenCode** (`opencode acp`) | 1.3.2 | Yes | Unknown | TBD |
| **Claude Code** | 2.1.112 | No | N/A | N/A |
| **Codex** | 0.36.0 | No | N/A | N/A |
| **Crush** | 0.62.1 | No | N/A | N/A |

**Conclusion**: Copilot, Droid, and OpenCode are the viable targets. Others may add ACP support later.

## 4. Architecture

### 4.1 Component Overview

```
┌──────────────────────────────────────────────────┐
│  ggcode (ACP Host)                                │
│                                                    │
│  ┌─────────────┐    ┌──────────────────────────┐ │
│  │ Discovery    │    │ ACP Client Manager       │ │
│  │ (startup)    │───▶│                          │ │
│  │              │    │  ┌─────────┐ ┌────────┐ │ │
│  └─────────────┘    │  │Client #1│ │Client#2│ │ │
│                      │  │copilot  │ │droid   │ │ │
│                      │  └────┬────┘ └───┬────┘ │ │
│  ┌─────────────┐    │       │           │       │ │
│  │ Tool:       │    └───────┼───────────┼───────┘ │
│  │ delegate    │────────────┘           │         │
│  │             │────────────────────────┘         │
│  └─────────────┘                                  │
└──────────────────────────────────────────────────┘
        │               │
   stdin/stdout     stdin/stdout
        │               │
  ┌─────▼─────┐  ┌──────▼──────┐
  │ copilot   │  │ droid       │
  │ --acp     │  │ --acp       │
  └───────────┘  └─────────────┘
```

### 4.2 New Files

```
internal/acp/
  client.go          # ACP Client — connects to an external agent process
  client_manager.go  # Manages all discovered ACP clients, lifecycle
  discovery.go       # Scans $PATH for known ACP agents
internal/tool/
  delegate.go        # Tool implementation: delegate_to_{agent_name}
```

### 4.3 Data Flow: User Prompt → Agent

```
User: "让 copilot 分析一下这个文件的性能问题"
  │
  ▼
ggcode LLM → tool_call: delegate(agent="copilot", prompt="分析这个文件的性能问题")
  │
  ▼
delegate tool → ClientManager.Get("copilot")
  │
  ▼
Client.Prompt(ctx, prompt) ──stdin──▶ copilot --acp
  │                                      │
  │  ◀── session/update notifications ───│
  │         (agent_message_chunk,        │
  │          tool_call, tool_call_update) │
  │                                      │
  │  ──▶ fs/read_text_file response ────▶│  (host serves agent's FS requests)
  │  ──▶ session/request_permission ────▶│  (host auto-approves or escalates)
  │                                      │
  │  ◀── prompt response ◀──────────────│
  │
  ▼
tool.Result → LLM context → User sees response
```

## 5. Detailed Design

### 5.1 Agent Discovery (`internal/acp/discovery.go`)

Scans `$PATH` for known ACP-compatible CLIs at startup.

```go
// AgentDef describes a known ACP-compatible CLI tool.
type AgentDef struct {
    Name        string   // canonical name: "copilot", "droid", "opencode"
    Title       string   // display name: "GitHub Copilot", "Droid"
    Binaries    []string // candidate binary names to search in $PATH
    ACPCommand  []string // args to start ACP mode, e.g., ["--acp"] or ["acp"]
    Description string   // short description for the tool registry
}

// KnownAgents is the built-in registry of known ACP agents.
var KnownAgents = []AgentDef{
    {
        Name:       "copilot",
        Title:      "GitHub Copilot",
        Binaries:   []string{"copilot"},
        // Copilot CLI is typically launched via `gh copilot --acp`
        // But the binary is `copilot` when installed standalone
        ACPCommand: []string{"--acp"},
        Description: "GitHub Copilot coding assistant",
    },
    {
        Name:       "droid",
        Title:      "Droid (Factory)",
        Binaries:   []string{"droid"},
        ACPCommand: []string{"--acp"},
        Description: "Droid AI coding agent by Factory",
    },
    {
        Name:       "opencode",
        Title:      "OpenCode",
        Binaries:   []string{"opencode"},
        ACPCommand: []string{"acp"},
        Description: "OpenCode terminal-based coding agent",
    },
}

// DiscoveredAgent represents an agent found on the system.
type DiscoveredAgent struct {
    Def    AgentDef
    Path   string    // absolute path to the binary
}

// Discover scans $PATH for known ACP agents.
// Returns only agents whose binary is found.
func Discover() []DiscoveredAgent { ... }
```

**Discovery process**:
1. Iterate `KnownAgents`
2. For each, search `$PATH` for any of its `Binaries`
3. Skip if not found
4. Return list of `DiscoveredAgent`

**Lazy initialization**: Discovery only finds the binary. The actual ACP handshake happens on first use (or eagerly if configured).

### 5.2 ACP Client (`internal/acp/client.go`)

The core client that manages a single ACP agent process.

```go
// Client manages a single ACP agent process.
// It handles lifecycle (start/stop), session management, and prompt execution.
type Client struct {
    def     DiscoveredAgent
    cmd     *exec.Cmd
    transport *Transport

    // State
    mu          sync.Mutex
    initialized bool
    caps        AgentCapabilities  // from initialize response
    authMethods []AuthMethod
    sessionID   string
    running     bool

    // Config
    workingDir  string
    onPermission func(ctx context.Context, req RequestPermissionRequest) (RequestPermissionResponse, error)

    // Goroutine management
    cancelRead context.CancelFunc
    done       chan struct{}
}

// Start launches the agent process and performs ACP initialize handshake.
func (c *Client) Start(ctx context.Context) error {
    // 1. exec.Command(c.def.Path, c.def.ACPCommand...)
    // 2. Wire stdin/stdout to Transport
    // 3. Send initialize request
    // 4. Store capabilities
    // 5. Start background read goroutine
    // 6. If auth required → handle auth
}

// NewSession creates a new session on the agent.
func (c *Client) NewSession(ctx context.Context, cwd string) error {
    // Send session/new → store sessionID
}

// Prompt sends a prompt and collects the full response.
// Blocks until the agent completes (end_turn, error, etc.).
// Returns the collected text and any tool call summaries.
func (c *Client) Prompt(ctx context.Context, prompt string) (*PromptResult, error) {
    // 1. Send session/prompt
    // 2. Read loop: collect session/update notifications
    //    - agent_message_chunk → append to text buffer
    //    - tool_call → log, auto-approve if configured
    //    - tool_call_update → track progress
    //    - fs/read_text_file → serve from local filesystem
    //    - fs/write_text_file → serve or reject based on policy
    //    - session/request_permission → auto-approve or escalate
    // 3. Return when prompt response arrives (StopReason)
}

// PromptResult is the aggregated result of a prompt execution.
type PromptResult struct {
    Text       string            // agent's text response
    StopReason StopReason        // why the agent stopped
    ToolCalls  []ToolCallSummary // summary of tool calls made
}

// ToolCallSummary is a simplified view of a tool call for display.
type ToolCallSummary struct {
    Name   string
    Title  string
    Status string // "completed", "failed"
}

// Close sends session/close and kills the process.
func (c *Client) Close() error { ... }
```

#### 5.2.1 The Read Loop

The client runs a background goroutine that reads messages from the agent process. This is the inverse of our server's `Handler.Run()`:

```go
func (c *Client) readLoop(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        req, resp, err := c.transport.ReadAnyMessage()
        if err != nil {
            if errors.Is(err, io.EOF) {
                close(c.done)
                return
            }
            continue
        }

        // Response to our request (e.g., session/prompt response)
        if resp != nil {
            c.transport.DeliverResponse(resp)
            continue
        }

        // Request FROM the agent (e.g., fs/read_text_file, session/request_permission)
        if req != nil {
            c.handleAgentRequest(ctx, req)
        }
    }
}

func (c *Client) handleAgentRequest(ctx context.Context, req *JSONRPCRequest) {
    switch req.Method {
    case "session/update":
        // Notification — collect into prompt result buffer
        c.handleSessionUpdate(req.Params)
    case "fs/read_text_file":
        // Agent wants to read a file — serve from local filesystem
        c.handleFSRead(ctx, req)
    case "fs/write_text_file":
        // Agent wants to write a file — apply locally
        c.handleFSWrite(ctx, req)
    case "session/request_permission":
        // Agent asks for permission — auto-approve or escalate
        c.handlePermission(ctx, req)
    case "terminal/create":
        // Agent wants to run a command
        c.handleTerminalCreate(ctx, req)
    case "terminal/output":
        c.handleTerminalOutput(ctx, req)
    default:
        // Unknown request — respond with error
        c.transport.WriteError(req.ID, -32601, "method not found: "+req.Method)
    }
}
```

#### 5.2.2 FS Proxying

When the agent requests `fs/read_text_file`, the client serves the file from the local filesystem (respecting sandbox/allowed_dirs):

```go
func (c *Client) handleFSRead(ctx context.Context, req *JSONRPCRequest) {
    var params ReadTextFileRequest
    json.Unmarshal(req.Params, &params)

    // Security: resolve and check path is within allowed dirs
    absPath := filepath.Resolve(c.workingDir, params.Path)

    data, err := os.ReadFile(absPath)
    if err != nil {
        c.transport.WriteError(req.ID, -32000, err.Error())
        return
    }

    c.transport.WriteResponse(req.ID, ReadTextFileResponse{Content: string(data)})
}
```

#### 5.2.3 Permission Handling

Two modes for agent permission requests:

1. **Auto-approve** (default): Trust the agent to execute tools autonomously. Suitable for read-only or well-known agents.
2. **Escalate to user**: Forward the permission request to ggcode's own approval handler (TUI dialog / WebUI prompt).

```go
func (c *Client) handlePermission(ctx context.Context, req *JSONRPCRequest) {
    var params RequestPermissionRequest
    json.Unmarshal(req.Params, &params)

    if c.onPermission != nil {
        // Custom handler (e.g., escalate to user)
        result, err := c.onPermission(ctx, params)
        if err != nil {
            c.transport.WriteError(req.ID, -32000, err.Error())
            return
        }
        c.transport.WriteResponse(req.ID, result)
        return
    }

    // Default: auto-approve
    c.transport.WriteResponse(req.ID, RequestPermissionResponse{
        Outcome: RequestPermissionOutcome{
            Outcome: "selected",
            SelectedOption: &SelectedPermissionOutcome{
                OptionID: "allow",
            },
        },
    })
}
```

### 5.3 Client Manager (`internal/acp/client_manager.go`)

Manages all discovered and active ACP clients.

```go
// ClientManager manages the lifecycle of all ACP agent clients.
type ClientManager struct {
    clients map[string]*Client  // keyed by agent name
    mu      sync.RWMutex

    // Configuration
    workingDir    string
    onPermission  func(ctx context.Context, req RequestPermissionRequest) (RequestPermissionResponse, error)
}

// NewClientManager discovers and prepares (but does not start) ACP clients.
func NewClientManager(workingDir string, onPermission func(...) (...)) *ClientManager {
    mgr := &ClientManager{...}

    // Discover agents
    agents := Discover()
    for _, agent := range agents {
        mgr.clients[agent.Def.Name] = NewClient(agent, workingDir, onPermission)
    }

    return mgr
}

// Available returns the list of available agent names.
func (m *ClientManager) Available() []string { ... }

// Get returns a client by name, starting it if needed.
func (m *ClientManager) Get(ctx context.Context, name string) (*Client, error) {
    // Lazy init: start the process + initialize on first use
}

// CloseAll shuts down all running agent processes.
func (m *ClientManager) CloseAll() { ... }
```

### 5.4 Delegate Tool (`internal/tool/delegate.go`)

A single tool that the LLM calls to delegate tasks to external agents.

```go
// DelegateTool delegates a task to an external ACP agent.
type DelegateTool struct {
    Manager *acp.ClientManager
}

func (t DelegateTool) Name() string { return "delegate" }

func (t DelegateTool) Description() string {
    return `Delegate a task to an external AI coding agent.

Available agents (auto-detected from your system):
` + t.buildAgentList() + `

Use this when:
- You want a second opinion from a different AI model
- The user explicitly asks a specific agent to do something
- You want to leverage agent-specific capabilities

The agent will execute the task autonomously and return the result.`
}

func (t DelegateTool) Parameters() json.RawMessage {
    return json.RawMessage(`{
        "type": "object",
        "properties": {
            "agent": {
                "type": "string",
                "enum": [` + t.buildAgentEnum() + `],
                "description": "The agent to delegate to"
            },
            "prompt": {
                "type": "string",
                "description": "The task description to send to the agent. Be specific and include all necessary context."
            },
            "working_directory": {
                "type": "string",
                "description": "Working directory for the agent (defaults to current directory)"
            }
        },
        "required": ["agent", "prompt"]
    }`)
}

func (t DelegateTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
    var params struct {
        Agent  string `json:"agent"`
        Prompt string `json:"prompt"`
        CWD    string `json:"working_directory"`
    }
    json.Unmarshal(input, &params)

    client, err := t.Manager.Get(ctx, params.Agent)
    if err != nil {
        return tool.Result{}, fmt.Errorf("agent %q not available: %w", params.Agent, err)
    }

    result, err := client.Prompt(ctx, params.Prompt)
    if err != nil {
        return tool.Result{Content: fmt.Sprintf("Agent %q error: %v", params.Agent, err), IsError: true}, nil
    }

    // Build result with agent attribution
    output := fmt.Sprintf("[Response from %s]\n\n%s", params.Agent, result.Text)
    if len(result.ToolCalls) > 0 {
        output += "\n\nTool calls made:"
        for _, tc := range result.ToolCalls {
            output += fmt.Sprintf("\n  - %s: %s (%s)", tc.Name, tc.Title, tc.Status)
        }
    }

    return tool.Result{Content: output}, nil
}
```

## 6. Startup Integration

### 6.1 Registration in `cmd/ggcode/root.go`

After existing tool registration (line ~462):

```go
// Discover and register ACP agent clients
acpMgr := acp.NewClientManager(workingDir, nil) // nil = auto-approve permissions
if len(acpMgr.Available()) > 0 {
    _ = registry.Register(tool.DelegateTool{Manager: acpMgr})
    debug.Log("startup", "discovered ACP agents: %v", acpMgr.Available())
}
```

### 6.2 Shutdown Integration

Add to the existing shutdown cascade (alongside `subAgentMgr.CancelAll()`, `swarmMgr.CancelAll()`):

```go
acpMgr.CloseAll()
```

## 7. Key Design Decisions

### 7.1 Single `delegate` Tool vs. Multiple `delegate_to_{name}` Tools

**Decision**: Single `delegate` tool with `agent` parameter.

**Rationale**:
- The available agent set varies per machine — a single tool with dynamic `enum` is cleaner
- LLM only needs to know about one tool
- Agent availability is reflected in the `enum` field of the JSON Schema
- If no agents are found, the tool is simply not registered

**Alternative considered**: Register `delegate_to_copilot`, `delegate_to_droid` etc. as separate tools.
- Rejected because it bloats the tool list and makes discovery harder for the LLM.

### 7.2 Lazy vs. Eager Initialization

**Decision**: Lazy — start agent process on first `delegate` call.

**Rationale**:
- Starting 3 agent processes at ggcode launch adds 2-5 seconds
- Users may not use the feature in every session
- Agent processes consume memory even when idle

**Tradeoff**: First call has ~1-2 second latency for process start + initialize handshake.
Mitigated by showing a status message like `[Starting copilot agent...]`.

### 7.3 Session Persistence

**Decision**: Do NOT persist ACP agent sessions across ggcode restarts.

**Rationale**:
- Agent processes are ephemeral (started/stopped with ggcode)
- Copilot CLI doesn't support `session/resume` via ACP anyway
- Adds complexity with minimal benefit
- Each `delegate` call creates a fresh session

**Future extension**: If resume becomes important, we can store the agent's session ID and call `session/resume` (for agents that support it like Droid).

### 7.4 Permission Policy

**Decision**: Auto-approve by default, configurable escalation.

**Rationale**:
- External agents are trusted tools (user installed them)
- Constant permission popups would make the feature unusable
- Users who want control can configure `acp.permission_mode: "ask"` in `ggcode.yaml`

### 7.5 Cost Attribution

**Decision**: External agents use their own API keys and billing.

**Rationale**:
- Copilot uses the user's GitHub Copilot subscription
- Droid uses Factory's API key (bundled)
- ggcode does not pay for external agent token usage
- ggcode's own token usage tracking does not apply

## 8. Configuration

### 8.1 `ggcode.yaml` Extension

```yaml
# ACP Client configuration
acp_client:
  # Enable/disable the feature (default: true if agents found)
  enabled: true

  # Permission mode for external agent tool calls
  # "auto" (default) - auto-approve all agent actions
  # "ask" - escalate agent permission requests to the user
  permission_mode: "auto"

  # Explicitly enable/disable specific agents
  agents:
    copilot:
      enabled: true
    droid:
      enabled: true
    opencode:
      enabled: false

  # Timeout for agent responses (default: 5 minutes)
  timeout: 5m
```

### 8.2 Additional Agent Definitions

Users can add custom ACP agents not in the built-in list:

```yaml
acp_client:
  custom_agents:
    - name: "my-agent"
      binary: "/path/to/my-agent"
      args: ["--acp"]
      description: "My custom ACP agent"
```

## 9. Edge Cases and Error Handling

### 9.1 Agent Crash During Prompt

If the agent process crashes mid-prompt:
1. `ReadAnyMessage()` returns `io.EOF`
2. Return partial result (text collected so far) with `StopReason: "error"`
3. Log the error
4. Mark client as `running: false`
5. Next call will restart the process

### 9.2 Agent Requires Authentication

Some agents (Copilot) may require GitHub OAuth:
1. Agent returns `authMethods` in `initialize` response
2. If agent is not authenticated, it will send appropriate auth error
3. Client returns the error to the user via tool result
4. User needs to authenticate the agent independently (e.g., `copilot login`)

**Decision**: Do NOT handle agent auth in ggcode. The agent is responsible for its own auth flow. ggcode just reports the auth error.

### 9.3 Multiple Concurrent Delegates

The LLM may call `delegate` multiple times in one turn (e.g., "让 copilot 和 droid 分别分析这个文件"):

- Each `delegate` call gets its own `Client.Prompt()` execution
- Multiple clients can run concurrently
- But each client handles one prompt at a time (ACP protocol is sequential per session)
- Consider: use separate sessions for concurrent calls, or serialize per-agent

**Implementation**: `ClientManager` maintains one `Client` per agent. If a second call comes while the first is still running, block until the first completes (or return error).

### 9.4 Context Cancellation

When the user presses Ctrl+C:
1. ggcode's interrupt handler calls `acpMgr.CloseAll()`
2. Each client sends `session/cancel` notification, then kills the process
3. Any blocked `Prompt()` calls return with `context.Canceled`

## 10. Testing Strategy

### 10.1 Unit Tests

| Test | Description |
|------|-------------|
| `TestDiscover` | Mock `$PATH` with known binaries, verify discovery |
| `TestDiscoverEmpty` | No agents found → empty list |
| `TestClientInitialize` | Mock agent process, verify handshake |
| `TestClientPrompt` | Mock agent returning text chunks + end_turn |
| `TestClientFSRead` | Agent requests `fs/read_text_file` → served correctly |
| `TestClientPermission` | Agent requests permission → auto-approved |
| `TestClientCrash` | Agent crashes mid-prompt → partial result returned |
| `TestDelegateTool` | Execute delegate tool → correct result format |

### 10.2 Integration Tests

| Test | Description |
|------|-------------|
| `TestCopilotE2E` | Start real `copilot --acp`, send a prompt, collect response |
| `TestDroidE2E` | Start real `droid --acp`, send a prompt, collect response |
| `TestDiscoveryReal` | Run discovery on actual system |

Tagged with `//go:build integration` — only run when real agents are available.

## 11. File Change Summary

| File | Action | Description |
|------|--------|-------------|
| `internal/acp/discovery.go` | **New** | Agent discovery via `$PATH` scan |
| `internal/acp/client.go` | **New** | ACP Client: process management, session lifecycle, prompt execution |
| `internal/acp/client_manager.go` | **New** | Client lifecycle manager |
| `internal/tool/delegate.go` | **New** | `delegate` tool implementation |
| `cmd/ggcode/root.go` | **Modify** | Register DelegateTool + ClientManager |
| `internal/acp/transport.go` | **No change** | Already supports bidirectional communication |
| `internal/acp/types.go` | **No change** | All protocol types already defined |

## 12. Future Extensions

### 12.1 Per-Agent Tools

Instead of a single `delegate` tool, register per-agent tools for better LLM targeting:

```
tool: ask_copilot    — delegate to GitHub Copilot
tool: ask_droid      — delegate to Droid
```

This allows per-agent descriptions that include the agent's specific capabilities (e.g., "Copilot is good at GitHub workflows, Droid excels at refactoring").

### 12.2 Agent-Initiated Conversation

Allow the agent to call back into ggcode's tool set (reverse delegation). This would make ggcode a tool host for the agent, not just a prompt router.

### 12.3 Streaming to TUI

Stream the agent's `session/update` notifications to the ggcode TUI in real-time, rather than blocking until the full response is collected. The user would see the agent "typing" in real-time.

### 12.4 Agent Session Resume

For agents that support `session/resume` (like Droid), persist the session ID and resume across `delegate` calls. This would give the agent conversation continuity.

### 12.5 Agent Marketplace

Support a user-configurable list of custom ACP agents (beyond the built-in known list), enabling an ecosystem of pluggable agents.
