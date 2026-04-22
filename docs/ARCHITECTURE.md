# ggcode — Architecture

> This document is for maintainers, contributors, and anyone who wants the internal technical layout.
> If you want to install and use ggcode as a product, start with the main [README](../README.md) first.

> Module: `github.com/topcheer/ggcode`
> Last updated: 2026-04-19

## Overview

ggcode is a terminal-based AI coding agent written in Go. It provides an interactive REPL where users describe coding tasks in natural language; the agent iteratively plans, calls tools, and refines its work in an agentic loop.

The core agent loop is now complemented by a lightweight **harness control plane**. Harness mode is intentionally implemented around the existing runtime rather than inside it: `ggcode harness ...` scaffolds repo guidance, generates nested subsystem `AGENTS.md` files, runs invariant checks, tracks orchestrated work items, queues multi-step work, supports dependency-gated backlogs, binds tasks to bounded contexts when useful, carries lightweight context ownership metadata, summarizes queue health per context, exposes owner-centric actionable inboxes and owner-filtered batch actions, creates isolated git worktrees when available, can explicitly retry failed backlog items or resume interrupted runs, uses pollable sub-agent-backed workers for queued execution, persists post-run delivery evidence, exposes review/approval, promotion, and owner/context-scoped release-batch loops on verified tasks, and can further split release-ready work into owner- or context-grouped rollout waves with persisted staged rollout state, explicit environment tags, gate approval/rejection, and advance/pause/resume/abort controls without forking a second agent architecture.

### Core Principles

- **Agentic loop**: user prompt → LLM → tool calls → execute → feed results back → repeat
- **Extensible tools**: built-in tools + MCP servers + Go plugin interface
- **Safe by default**: permission policy with path sandbox and dangerous-command detection
- **Streaming UX**: Bubble Tea TUI with live markdown, diff preview, spinners
- **Portable config**: YAML with `${ENV_VAR}` expansion

## Directory Structure

```
cmd/ggcode/              # CLI entrypoint (main.go, root.go, pipe.go)
internal/
  agent/                 # Agent: agentic loop, provider abstraction
    agent.go             # Agent struct, Run/RunStream, core orchestration
    agent_autopilot.go   # Autopilot continuation logic
    agent_compact.go     # Auto-compaction of conversation history
    agent_memory.go      # Memory management helpers
    agent_tool.go        # Tool execution, diff confirm, hooks
    agent_test.go
  checkpoint/            # File edit checkpoints for undo
    checkpoint.go
  commands/              # Markdown-backed skills and legacy custom slash commands
    command.go           # Command/skill metadata and expansion helpers
    loader.go            # Load skills and legacy commands from ~/.agents, ~/.ggcode, and project .ggcode
  config/                # Configuration loading and env expansion
    config.go            # Config struct, LoadFromFile
    env.go               # ${ENV_VAR} expansion
  context/               # Conversation context management
    manager.go           # ContextManager: message history, compression
    tokenizer.go         # CJK-aware token estimation
  cost/                  # Token usage and cost tracking
    manager.go           # CostManager: per-session and total cost
    pricing.go           # Model pricing data
    types.go             # SessionCost type
    tracker.go           # In-flight token counting
  debug/                 # Debug logging
    debug.go
  diff/                  # Diff formatting utilities
    diff.go              # FormatDiff, IsDiffContent
  hooks/                 # Pre/post execution hooks
    hook.go              # Hook struct
    runner.go            # HookRunner
  harness/               # Harness control plane: scaffold, checks, queue/run tracking, review/promotion/release, worktrees, gc
    config.go            # Harness config model and defaults
    project.go           # Project discovery and scaffold creation
    check.go             # Structural checks and validation commands
    run.go               # Tracked harness runs / queued execution via the existing ggcode binary
    release.go           # Release-batch planning and persisted release reports
    worktree.go          # Git worktree lifecycle for isolated task workspaces
    gc.go                # Archive/prune stale harness state
  image/                 # Image file handling for multimodal input
    image.go             # ReadFile, Placeholder
  mcp/                   # Model Context Protocol client
    client.go            # MCPClient: spawn and communicate with MCP servers
    adapter.go           # Tool adapter (MCP tool → ggcode tool interface)
    jsonrpc.go           # JSON-RPC protocol
    oauth.go             # OAuth 2.1 handler: metadata discovery, DCR, device flow, token refresh
  memory/                # Project and auto memory
    auto.go              # AutoMemory: automatic memory extraction
    project.go           # ProjectMemory: load memory files
  permission/            # Permission and sandbox policy
    policy.go            # PermissionPolicy interface
    config_policy.go     # Config-backed policy
    mode.go              # PermissionMode enum (Supervised/Plan/Auto/Bypass)
    dangerous.go         # Dangerous command detection
    sandbox.go           # Path sandbox enforcement
  plugin/                # Go plugin system
    loader.go            # Plugin loader
    mcp_loader.go        # MCP server plugin loader
    plugin.go            # Plugin interface
  provider/              # LLM provider implementations
    provider.go          # Provider interface
    openai.go            # OpenAI-compatible provider
    anthropic.go         # Anthropic provider
    gemini.go            # Google Gemini provider
    copilot.go           # GitHub Copilot provider
    registry.go          # Provider registry
    retry.go             # Retry logic with backoff
  session/               # Session persistence
    store.go             # Store: save/load sessions as JSONL
  subagent/              # Sub-agent spawning and management
    manager.go           # Manager: spawn, list, cancel, and snapshot sub-agents
    runner.go            # Runner: execute sub-agent tasks and wait/poll their state
  tool/                  # Built-in tools
    tool.go              # Tool interface
    builtin.go           # RegisterBuiltinTools
    read_file.go         # File reading
    write_file.go        # File writing
    edit_file.go         # File editing with checkpoint support
    run_command.go       # Synchronous shell command execution
    command_jobs.go      # Background command job manager and buffered output
    command_job_tools.go # Async command start/read/wait/write/stop/list tools
    search_files.go      # Code search
    list_dir.go          # Directory listing
    glob.go              # Glob pattern matching
    web_fetch.go         # HTTP fetch with SSRF protection
    web_search.go        # Web search
    git_diff.go / git_log.go / git_status.go  # Git tools
    save_memory.go       # Save memory entries
    todo_write.go        # Write todo lists
    spawn_agent.go       # Spawn sub-agents
    list_agents.go / wait_agent.go  # Sub-agent polling and wait tools
  tui/                   # Terminal UI (Bubble Tea)
    model.go             # Model struct, Init, configuration methods
    model_update.go      # Update loop: message routing, key handling, agent lifecycle
    model_messages.go    # Message types (streamMsg, doneMsg, errMsg, etc.)
    model_approval.go    # Approval/diff confirmation selection lists
    model_pending.go     # Pending state helpers (device codes, questionnaires)
    model_clipboard.go   # Clipboard image loading
    model_terminal.go    # Terminal utility helpers (open URL, resize)
    view.go              # View rendering, status bar, autocomplete
    commands.go          # Slash command handlers
    submit.go            # Message submission and agent startup
    resize.go            # Window resize handling
    repl.go              # REPL: wires Model to Agent, session, cost
    completion.go        # Slash command autocomplete logic
    viewport.go          # Scrollable viewport with auto-follow
    spinner.go           # Tool execution spinner
    diff.go              # Diff display formatting
    markdown.go          # Markdown rendering with glamour
    preview_panel.go     # File preview, markdown rendering, syntax highlighting
    app.go               # Minimal package marker
  util/                  # Shared utilities
    truncate.go          # String truncation
docs/                    # Documentation
  ARCHITECTURE.md        # This file
```

## Key Patterns

- **Bubble Tea streaming**: Agent runs in a goroutine; events flow into the TUI via `tea.Program.Send()`
- **Permission policy**: Two layers — tool-level `ShouldAsk` + dangerous-command detection
- **Import cycle avoidance**: Shared types defined in downstream packages; factory functions injected
- **MCP client**: Spawns fresh process per tool call (`callToolStandalone`); HTTP transport supports OAuth 2.1 with automatic metadata discovery, dynamic client registration, device flow, and token refresh
- **Provider SDKs**: OpenAI (go-openai), Anthropic (anthropic-sdk-go), Gemini (genai), Copilot (custom transport on top of the provider abstraction)
- **IM routing**: IM events are fanned out to all bound adapters; per-channel echo suppression skips the originating adapter for user mirror messages
- **Session format**: JSONL with index.json metadata

## Conversation Context Management

### Auto-Compact Strategy

The agent monitors conversation token usage and triggers compaction when it approaches the model's context window limit. Compaction has two levels:

1. **Microcompact** — Truncates large tool results by preserving the first 10 and last 5 lines, with individual lines truncated to 200 characters. This is fast and cheap but only saves tokens from tool output.

2. **Summarize** — When microcompact is insufficient, older messages are replaced with an LLM-generated summary. The summary prompt is optimized for coding contexts: it preserves key decisions, file paths, code structure, error resolutions, and pending work. Tool results in the summary payload are pre-truncated to 500 characters to prevent the summary request itself from triggering context overflow.

Thresholds:
- With usage baseline: 75% of context window triggers compaction
- Without baseline: 65% triggers compaction
- Target after compaction: 55% of context window

### Session Checkpoints

After summarize compaction, the compacted message state is persisted as a **checkpoint** record in the session JSONL file. This enables efficient session recovery:

```
Session JSONL:
  msg1 → msg2 → ... → msg50
  [checkpoint: 3 compacted messages, 500 tokens]
  msg51 → msg52 → ...
```

On `--resume`, the loader finds the latest checkpoint and only loads:
1. Messages from the checkpoint snapshot
2. Messages recorded after the checkpoint

This avoids re-loading and re-compacting the entire conversation history. Checkpoints are triggered by all compaction paths:
- `maybeAutoCompact` (periodic check at loop start)
- `tryReactiveCompact` (after prompt-too-long errors)
- `forceCompactAndPause` (autopilot loop guard)

### IM Tool Call Display

All IM adapters (Telegram, QQ, Discord, Slack, DingTalk, Feishu) share a unified tool result formatter (`internal/im/tool_format.go`). Each built-in tool has a dedicated format with emoji icon + code block:

| Category | Format |
|----------|--------|
| Commands | `✓` + bash code block for command + plain code block for output (no truncation) |
| File read | `✓ 📖 {path}` (status only, no content) |
| File edit/write | `✓ ✏️/📝 {path}` (status only) |
| Directory/glob/search | Icon + pattern + full results in code block |
| Git | `✓ 🔧 Git Status/Log/Diff` + output in code block |
| Web | `✓ 🌐` + output in code block |
| MCP tools | `✓ 🔧 PrettyName(args)` + output in code block |
| Error variants use `✗` + error in code block |

All absolute paths are relativized against the project working directory before sending to IM.

### Provider Error Detection

All provider adapters detect output truncation and policy errors:
- **OpenAI**: `finish_reason=length` returns error
- **Anthropic**: `stop_reason=max_tokens` or `stop_reason=refusal` returns error
- **Gemini**: `FinishReason=MAX_TOKENS`, `SAFETY`, `RECITATION`, etc. returns error

Default `max_output_tokens` is 16384 (configurable per endpoint).
