# ggcode — Architecture Design Document

> Module: `github.com/topcheer/ggcode`
> Version: v0.1.0-draft
> Last updated: 2026-04-03

## 1. Overview

ggcode is a terminal-based AI coding agent written in Go, inspired by modern AI coding assistants. It provides an interactive REPL where users describe coding tasks in natural language; the agent iteratively plans, calls tools (file I/O, shell commands, search), and refines its work in a loop until the task is complete.

### Core Principles

- **Agentic loop**: user prompt → LLM → tool calls → execute → feed results back → repeat until done
- **Extensible tools**: built-in tool set + MCP servers + Go plugin interface
- **Safe by default**: permission policy (ask/allow/deny) with path sandbox and dangerous-command detection
- **Streaming UX**: Bubble Tea TUI with live markdown, diff preview, spinners, and keyboard shortcuts
- **Portable config**: YAML with `${ENV_VAR}` expansion, no plaintext secrets

---

## 2. High-Level Architecture

```
┌──────────────────────────────────────────────────────────┐
│                        TUI (Bubble Tea)                   │
│  REPL · Markdown render · Diff preview · Spinner · Input  │
└──────────────┬───────────────────────────────┬────────────┘
               │ commands / events              │ streaming
               ▼                               ▼
┌──────────────────────┐            ┌──────────────────────┐
│       Agent          │◄───────────│   Event Bus (chan)   │
│  agentic loop core   │            │  StreamEvent, etc.   │
└──┬──────┬──────┬─────┘            └──────────────────────┘
   │      │      │
   ▼      ▼      ▼
┌─────┐┌──────┐┌──────────┐
│Tool ││ Ctx  ││ Session  │
│Exec ││Mgr   ││ Store    │
└──┬──┘└──────┘└──────────┘
   │
   ▼
┌──────────────────────────┐
│     Tool Registry         │
│  built-in · MCP · plugins │
└──────────────────────────┘
   │              │
   ▼              ▼
┌─────────┐  ┌──────────┐
│ Built-in │  │ MCP Client│
│ Tools    │  │ (JSON-RPC)│
└─────────┘  └──────────┘
               │
               ▼
         ┌──────────┐
         │ MCP Servers│
         │ (external) │
         └──────────┘

┌──────────────────────────┐
│     Provider Layer       │
│  OpenAI · Anthropic · Gemini│
└──────────────────────────┘
```

---

## 3. Project Structure

```
ggcode/
├── cmd/
│   └── ggcode/
│       ├── main.go              # entry point
│       └── root.go              # cobra root command, REPL bootstrap
├── internal/
│   ├── agent/
│   │   ├── agent.go             # Agent struct, Run() loop
│   │   └── loop.go              # agentic loop: send→tool→execute→feed
│   ├── provider/
│   │   ├── provider.go          # Provider interface + registry
│   │   ├── openai.go            # OpenAI-compatible (incl. Azure, local)
│   │   ├── anthropic.go         # Anthropic
│   │   └── gemini.go            # Google Gemini
│   ├── tool/
│   │   ├── tool.go              # Tool interface + Registry
│   │   ├── read_file.go
│   │   ├── write_file.go
│   │   ├── edit_file.go
│   │   ├── list_dir.go
│   │   ├── search_files.go
│   │   ├── glob.go
│   │   ├── run_command.go       # shell execution
│   │   ├── git.go               # git status, diff, log, commit
│   │   └── builtin.go           # registers all built-in tools
│   ├── mcp/
│   │   ├── client.go            # MCP client: connect, call, list
│   │   ├── transport.go         # stdio transport (JSON-RPC over pipes)
│   │   └── convert.go           # MCP tool → internal tool adapter
│   ├── permission/
│   │   ├── policy.go            # PermissionPolicy interface
│   │   ├── sandbox.go           # path sandbox rules
│   │   └── dangerous.go         # dangerous command detection
│   ├── context/                 # ⚠️ import as ctxpkg in consuming files
│   │   ├── manager.go           # ContextManager interface
│   │   ├── token_counter.go     # token counting (per-provider)
│   │   └── summarizer.go        # auto-summarize when approaching limit
│   ├── session/
│   │   ├── store.go             # SessionStore interface + disk impl
│   │   ├── session.go           # Session model
│   │   └── index.go             # session index browsing
│   ├── cost/
│   │   ├── tracker.go           # per-session token + cost tracking
│   │   └── pricing.go           # built-in pricing table
│   ├── config/
│   │   ├── config.go            # Config struct + Load/Save
│   │   ├── env.go               # ${ENV_VAR} expansion
│   │   └── watcher.go           # file watcher for hot reload
│   ├── tui/
│   │   ├── app.go               # Bubble Tea model (main)
│   │   ├── repl.go              # REPL input component
│   │   ├── markdown.go          # markdown renderer (glamour)
│   │   ├── diff.go              # diff preview renderer
│   │   ├── spinner.go           # tool execution spinner
│   │   ├── styles.go            # lipgloss styles
│   │   └── keys.go              # key bindings
│   └── plugin/
│       ├── loader.go            # Go plugin (.so) loader
│       └── api.go               # plugin interface definition
├── pkg/
│   └── mcp/
│       ├── types.go             # MCP protocol types (JSON-RPC)
│       └── schema.go            # JSON Schema utilities
├── docs/
│   ├── ARCHITECTURE.md
│   └── PHASE_PLAN.md
├── go.mod
├── go.sum
├── Makefile
├── README.md
└── ggcode.yaml                  # default config
```

---

## 4. Core Module Design

### 4.1 Provider Layer

Unified interface over multiple LLM backends. Each provider normalizes its SDK's types into ggcode's internal `Message` format.

**Key design decisions:**
- `Chat()` for non-streaming (used by token counter, cost estimation)
- `ChatStream()` returns a channel of `StreamEvent` — the agent loop consumes these
- Token counting is provider-specific (Anthropic has a real API; OpenAI/Gemini estimate via tiktoken or heuristic)
- Tool definitions are converted from ggcode's `ToolDefinition` to each provider's native format

**Stream event flow:**
```
Provider.ChatStream(messages) → chan StreamEvent
  ├── StreamEventText{Content: "..."}
  ├── StreamEventToolCall{Name, Arguments}   // accumulated (delta for OpenAI)
  ├── StreamEventToolCallDone{Name, Arguments}
  └── StreamEventDone{Usage: TokenUsage}
```

For OpenAI streaming, tool call deltas arrive by index and must be accumulated into complete calls before execution.

### 4.2 Agent Loop

```
User Input
  → ContextManager.Add(user message)
  → for {
        response := Provider.ChatStream(messages)
        if no tool calls → break, display to user
        for each tool call:
          if !Permission.ShouldAsk(tool) → execute directly
          else → ask user via TUI, wait for approval
          result := ToolRegistry.Execute(name, args)
          ContextManager.Add(tool_result)
      }
  → ContextManager.Add(assistant response)
  → Session.Store(conversation)
```

The loop runs in a goroutine. Communication with the TUI happens via an `EventBus` (Go channels). The agent sends `StreamEvent`s into the bus; the Bubble Tea model receives them via `tea.Program.Send()`.

**Stop conditions:**
- LLM returns a text-only response (no tool calls)
- User interrupts (Ctrl+C / `/exit`)
- Max iterations reached (configurable, default 50)
- Context window exhausted (summarizer triggered)

### 4.3 Tool System

**Interface:**
```go
type Tool interface {
    Name() string
    Description() string
    Parameters() json.RawMessage  // JSON Schema
    Execute(ctx context.Context, input json.RawMessage) (ToolResult, error)
}

type ToolResult struct {
    Content string
    IsError bool
}
```

**Built-in tools:**

| Tool | Purpose | Requires Approval |
|------|---------|-------------------|
| `read_file` | Read file contents | No |
| `write_file` | Create/overwrite file | Yes |
| `edit_file` | Targeted find/replace edit | Yes |
| `list_directory` | Directory listing | No |
| `search_files` | Grep/ripgrep search | No |
| `glob` | Filename pattern matching | No |
| `run_command` | Execute shell commands | Yes (dangerous check) |
| `git_status` | Git status/diff/log | No |

**Tool Registry:**
```go
type Registry struct {
    tools map[string]Tool
    mu    sync.RWMutex
}
func (r *Registry) Register(t Tool) error
func (r *Registry) Get(name string) (Tool, bool)
func (r *Registry) List() []Tool
func (r *Registry) ToDefinitions() []ToolDefinition  // for LLM
```

### 4.4 MCP Client

Connects to external MCP (Model Context Protocol) servers via stdio transport. Each server is defined in config:

```yaml
mcp_servers:
  - name: filesystem
    command: npx
    args: ["-y", "@anthropic/mcp-filesystem", "/tmp"]
```

**Transport:** spawn server process, communicate via stdin/stdout with JSON-RPC framing (Content-Length header + newline-delimited JSON).

**Flow:**
1. Initialize: `initialize` → server responds with capabilities
2. List tools: `tools/list` → convert MCP tool definitions to ggcode `Tool` adapters
3. Call tool: `tools/call` → forward result back to agent loop

**Adapter pattern:** `MCPToolAdapter` wraps an MCP tool call into the `Tool` interface, bridging the MCP protocol to ggcode's tool registry.

### 4.5 Permission System

Two-layer permission model:

**Layer 1 — Tool-level policy:**
```go
type PermissionPolicy interface {
    ShouldAsk(toolName string, input json.RawMessage) (bool, error)
}
```

Config controls per-tool default:
```yaml
permissions:
  read_file: allow
  write_file: ask
  run_command: ask
  search_files: allow
```

**Layer 2 — Dangerous command detection (run_command only):**
- Pattern matching for destructive commands: `rm -rf /`, `mkfs`, `dd if=`, `chmod 777 /`, `sudo rm`, `:(){:|:&};:`
- Regex-based, extensible deny list
- Always asks even if `run_command` is set to `allow`

**Path sandbox:**
- Restrict file operations to configurable allowed directories
- Default: current working directory + home directory
- Symlink resolution to prevent sandbox escape

### 4.6 Context Manager

Manages the conversation history sent to the LLM, keeping it within the provider's context window.

```go
type ContextManager interface {
    Add(role string, content Content) []ContentBlock
    Messages() []Message
    TokenCount() int
    MaxTokens() int
    Summarize(ctx context.Context, provider Provider) error
}
```

**Strategy:**
1. Maintain ordered list of messages with running token count
2. When `TokenCount()` approaches `MaxTokens() * 0.8`, trigger summarization
3. Summarization: send conversation to LLM with "summarize this conversation" system prompt, replace old messages with summary
4. Keep system prompt + last N tool results intact during summarization
5. Token counting: Anthropic uses `/v1/messages/count_tokens`; OpenAI/Gemini use tiktoken or character-based heuristic

### 4.7 Session Persistence

```
~/.ggcode/sessions/
├── 2026-04-03_abc123/
│   ├── meta.json        # {id, created, updated, title, provider, model}
│   ├── messages.jsonl   # one JSON line per message
│   └── cost.json        # token usage + cost summary
└── index.json           # session list for browsing
```

**Atomic writes:** write to `.tmp` file, then `os.Rename()` (atomic on POSIX).

**Operations:**
- `Save(session)` — append message, update index
- `Load(id)` — replay messages from JSONL
- `List()` — read index, sorted by date
- `Resume(id)` — load + set as current
- `Export(id, format)` — markdown or JSON export
- `Cleanup(maxAge)` — remove sessions older than threshold

### 4.8 Cost Tracker

```go
type CostTracker struct {
    provider   string
    model      string
    inputTokens  int64
    outputTokens int64
    cacheReadTokens int64
    cacheWriteTokens int64
    totalCost  float64
}
```

**Built-in pricing table** (update quarterly):
- claude-sonnet-4-20250514: $3/$15 per MTok
- gpt-4o: $2.50/$10 per MTok
- gemini-2.5-pro: $1.25/$10 per MTok
- Plus other models from each provider

Cost is displayed in the TUI status bar and in session metadata.

### 4.9 Configuration

```yaml
# ~/.ggcode/ggcode.yaml
provider: anthropic                    # default provider
model: claude-sonnet-4-20250514

providers:
  anthropic:
    api_key: ${ANTHROPIC_API_KEY}
    base_url: ""                       # optional override
    max_tokens: 8192
  openai:
    api_key: ${OPENAI_API_KEY}
    base_url: ""                       # e.g. Azure endpoint
    model: gpt-4o
  gemini:
    api_key: ${GEMINI_API_KEY}
    model: gemini-2.5-pro

system_prompt: |
  You are ggcode, an AI coding assistant...

permissions:
  default: ask
  tools:
    read_file: allow
    list_directory: allow
    search_files: allow
    glob: allow
    write_file: ask
    edit_file: ask
    run_command: ask
    git_status: allow

sandbox:
  allowed_paths:
    - .
    - ${HOME}

context:
  max_tokens: 200000
  summarize_threshold: 0.8

mcp_servers: []

cost:
  budget_warning: 10.0                # USD
  budget_limit: 50.0

session:
  auto_save: true
  directory: ~/.ggcode/sessions
  cleanup_days: 30
```

**Hot reload:** `fsnotify` watches config file; on change, debounce 500ms, reload, apply diff to running agent via `ApplyReloadable(old, new)` which preserves non-reloadable runtime state.

### 4.10 TUI (Bubble Tea)

**Components:**
- **App model** — root Bubble Tea model, manages layout and delegates to sub-components
- **REPL input** — text input with history (↑/↓), multi-line support (Shift+Enter), slash commands
- **Markdown renderer** — `glamour` for assistant responses, includes syntax highlighting via bundled `chroma`
- **Diff viewer** — unified diff rendering with +/- coloring, `@@` hunk detection
- **Tool spinner** — `tea.Tick`-based spinner showing which tool is running
- **Status bar** — model name, cost so far, session info, context usage

**Key bindings:**
- `Enter` — send message
- `Shift+Enter` — new line in input
- `Ctrl+C` — cancel current generation
- `Ctrl+D` — exit
- `/help` — show commands
- `/model <name>` — switch model
- `/compact` — force context summarization
- `/cost` — show cost breakdown
- `/sessions` — browse sessions
- `/clear` — clear conversation

**Streaming integration:**
The agent runs in a goroutine. It sends `StreamEvent`s via a channel. The TUI uses `tea.Program.Send()` (captured `*tea.Program` reference) to inject events into the Bubble Tea update loop. This avoids blocking the goroutine.

**Async approval flow:**
When a tool needs approval, the agent sends `ApprovalRequestEvent` through the event bus. The TUI displays the request and waits for user input (y/n/a). The approval response is sent back via a reply channel. The agent goroutine blocks on the reply channel — this is the only acceptable blocking point.

### 4.11 Plugin System

```go
// plugin/api.go — interface that plugins must implement
type Plugin interface {
    Name() string
    Tools() []tool.Tool
}
```

Plugins are compiled as Go `.so` files (using `plugin` build tag). At startup, ggcode scans a plugin directory and loads each `.so`:

```go
// plugin/loader.go
func LoadPlugins(dir string) ([]Plugin, error) {
    // glob *.so, plugin.Open, lookup "New" symbol
}
```

**Limitations:** Go plugins require same Go version and build flags. This is acceptable for a first implementation. Future versions may support WASM or subprocess-based plugins.

---

## 5. Interface Definitions (Go Code)

See the following files for concrete Go interface definitions:
- `internal/provider/provider.go` — Provider interface
- `internal/tool/tool.go` — Tool interface + Registry
- `internal/permission/policy.go` — PermissionPolicy interface
- `internal/context/manager.go` — ContextManager interface
- `internal/session/store.go` — SessionStore interface

---

## 6. Key Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| CLI framework | Cobra | Industry standard, well-documented, shell completion |
| TUI framework | Bubble Tea | Elm architecture, composable, good streaming support |
| Markdown rendering | glamour | Terminal markdown with syntax highlighting (bundles chroma) |
| Config format | YAML | Human-readable, supports comments, `gopkg.in/yaml.v3` |
| JSON Schema | jsonschema-go | For tool parameter validation |
| File watching | fsnotify | Cross-platform, well-maintained |
| MCP protocol | Custom (pkg/mcp) | JSON-RPC 2.0 over stdio, per spec |
| Testing | testify + httptest | Assertions + mock HTTP servers for providers |

---

## 7. Data Flow Summary

```
User types message in TUI
  → TUI sends UserInputEvent to Agent
  → Agent adds to ContextManager
  → Agent calls Provider.ChatStream(messages)
  → Provider streams StreamEvents
    → Text events → TUI renders markdown
    → ToolCall events → Agent executes via Registry
      → Permission check (async with TUI approval)
      → Tool.Execute() → result
      → Agent feeds result back to Provider
  → Agent stops (no more tool calls)
  → Session saves conversation
  → CostTracker updates
  → TUI shows prompt for next input
```

---

## 8. Error Handling Strategy

- **Provider errors**: retry with exponential backoff (max 3 retries), surface to user
- **Tool execution errors**: return as `ToolResult{IsError: true}`, let LLM decide how to handle
- **Permission denied**: return as tool result, LLM can ask for alternative approach
- **Context overflow**: auto-summarize, if still over → warn user and suggest `/compact`
- **Config errors**: fail fast at startup with clear error messages
- **MCP server errors**: log warning, mark server tools as unavailable, continue

---

## 9. Security Considerations

- **No auto-approve for destructive operations**: `run_command` always checks for dangerous patterns
- **Path sandbox**: file tools restricted to allowed directories with symlink resolution
- **Secret management**: API keys via `${ENV_VAR}` expansion only, never stored in config
- **Plugin isolation**: plugins loaded from user-controlled directory, run in same process (Go plugin limitation)
- **Input validation**: all tool inputs validated against JSON Schema before execution
