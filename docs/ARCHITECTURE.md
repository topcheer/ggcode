# ggcode — Architecture

> This document is for maintainers, contributors, and anyone who wants the internal technical layout.
> If you want to install and use ggcode as a product, start with the main [README](../README.md) first.

> Module: `github.com/topcheer/ggcode`
> Last updated: 2026-04-09

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
    agent.go             # Agent struct, Run/RunStream, tool execution
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
    model.go             # Model struct, Init, Update, msg types
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
    fullscreen.go        # Fullscreen mode (unused)
    app.go               # Package placeholder
  util/                  # Shared utilities
    truncate.go          # String truncation
docs/                    # Documentation
  ARCHITECTURE.md        # This file
```

## Key Patterns

- **Bubble Tea streaming**: Agent runs in a goroutine; events flow into the TUI via `tea.Program.Send()`
- **Permission policy**: Two layers — tool-level `ShouldAsk` + dangerous-command detection
- **Import cycle avoidance**: Shared types defined in downstream packages; factory functions injected
- **MCP client**: Spawns fresh process per tool call (`callToolStandalone`)
- **Provider SDKs**: OpenAI (go-openai), Anthropic (anthropic-sdk-go), Gemini (genai)
- **Session format**: JSONL with index.json metadata
