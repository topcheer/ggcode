# ggcode — Architecture

> This document is for maintainers, contributors, and anyone who wants the internal technical layout.
> If you want to install and use ggcode as a product, start with the main [README](../README.md) first.

> Module: `github.com/topcheer/ggcode`
> Last updated: 2026-07-15

## Overview

ggcode is a terminal-based AI coding agent written in Go. It provides an interactive REPL where users describe coding tasks in natural language; the agent iteratively plans, calls tools, and refines its work in an agentic loop. The same core engine powers a desktop GUI (Wails/React), a daemon mode with IM gateway, a harness control plane, an A2A (Agent-to-Agent) mesh, and a mobile relay tunnel.

The core agent loop is complemented by several subsystems:

- **Harness control plane** (`ggcode harness ...`): scaffolds repo guidance, generates nested subsystem `AGENTS.md` files, runs invariant checks, tracks orchestrated work items, queues multi-step work with dependency-gated backlogs, binds tasks to bounded contexts, summarizes queue health per context, exposes owner-centric actionable inboxes and batch actions, creates isolated git worktrees, persists delivery evidence, and exposes review/approval, promotion, and release-batch loops — all built around the existing runtime rather than forking a second agent architecture.
- **A2A mesh**: Multiple ggcode instances discover each other, authenticate via multiple schemes (API key, OAuth2+PKCE, Device Flow, OIDC, mTLS), and call tools across instances transparently via MCP bridge. Optional mDNS LAN discovery.
- **IM gateway**: Remote coding via 16 IM platforms (QQ, Telegram, Discord, Slack, DingTalk, Feishu, WeCom, WeChat, WhatsApp, Signal, Matrix, Mattermost, IRC, Nostr, Twitch, PC) with slash commands for adapter management.
- **Mobile relay**: WebSocket tunnel broker that records and replays agent events to mobile clients via a standalone relay server, enabling mobile/remote interaction with running sessions.
- **Desktop GUI**: Wails-based application (React frontend + Go backend) with visual chat, IM integration, tool approval dialogs, session sidebar, and LSP language server status panel.

### Core Principles

- **Agentic loop**: user prompt → LLM → tool calls → execute → feed results back → repeat
- **Extensible tools**: built-in tools + MCP servers + Go plugin interface
- **Safe by default**: permission policy with path sandbox and dangerous-command detection
- **Multi-surface**: TUI (Bubble Tea), Desktop (Wails/React), Daemon (headless + IM), Pipe (non-interactive), ACP (editor integration)
- **Portable config**: YAML with `${ENV_VAR}` expansion
- **Multi-instance collaboration**: A2A protocol with multi-auth, auto-discovery, MCP bridge
- **Mobile tunnel**: event persistence, replay-on-reconnect, active session tracking

## Directory Structure

```
cmd/ggcode/                # CLI entrypoint
  main.go                  # Entry point
  root.go                  # Root command: tool registration, agent wiring, permission modes
  pipe.go                  # Non-interactive pipe mode (-p flag)
  daemon.go                # Daemon mode: headless agent, follow display, tunnel/IM, keyboard shortcuts
  harness_cmd.go           # Harness CLI: scaffold, run, queue, review, promote, release
  im_cmd.go                # IM adapter management CLI
  mcp_cmd.go               # MCP server management CLI
  acp.go                   # ACP server CLI (expose ggcode as an ACP agent)
  resume_picker.go         # Session resume picker (interactive list)
  onboard.go               # First-run onboarding wizard
  llm_probe.go             # LLM connectivity probing
  bootstrap.go             # Provider bootstrap helpers

cmd/ggcode-installer/      # Standalone Go installer that downloads release binaries

desktop/                   # Desktop GUI application (Wails-based, separate Go module)
  ggcode-desktop-wails/    # Wails desktop app — React frontend + Go backend
    app.go                 # Wails App: all bound methods (chat, config, IM, MCP, LSP, tunnel, sessions, files)
    main.go                # Wails main entry point
    frontend/src/          # React + TypeScript SPA (Vite build)
      App.tsx              # Root component
      components/           # Chat view, sidebar, settings, tool approval, inspector, model picker
      i18n/                 # English and Chinese localization
      assets/              # Icons and images
  wailskit/                # Shared Go backend for Wails desktop
    chat.go                # ChatBridge: agent lifecycle, streaming, tool calls, tunnel integration
    config.go              # Config REST: vendors, endpoints, API keys, MCP, A2A, IM, LSP, general
    sessions.go            # Session list/load/delete, workspace filtering
    im.go                  # IM adapter management (enable, disable, mute, bind, unbind)
    mcp.go                 # MCP server management and status
    lsp.go                 # LSP server discovery, status, install (user/global/project scope)
    files.go               # File reading/serving for desktop preview
    logstream.go           # Log streaming for debugging
    desktop_config.go      # Desktop-specific config accessors

ggcode-relay/              # Standalone relay server for mobile tunnel
  relay.go                 # WebSocket relay: room management, event routing, peer-to-peer forwarding
  store.go                 # SQLite event persistence with dedup by sessionID+eventID
  share_auth.go            # Share session authentication and QR code pairing
  stats.go                 # Relay usage statistics
  trace.go                 # Request tracing
  model_catalog.go         # Model catalog for mobile client discovery
  Dockerfile               # Container deployment
  railway.json             # Railway deployment config

internal/
  agent/                   # Core agent loop
    agent.go               # Agent struct, Run/RunStream, provider call orchestration
    agent_autopilot.go     # Autopilot continuation logic (auto-resume on ask_user)
    agent_compact.go       # Auto-compaction: microcompact + summarize
    agent_precompact.go    # Pre-compaction heuristics and context inference
    agent_memory.go        # Memory management helpers (project/auto memory)
    agent_tool.go          # Tool execution, diff confirmation, pre/post hooks
    agent_prompt_inject.go  # Dynamic system prompt injection (lanchat peers, playbook hints)
    # Optimization layers (see Agent Optimization Stack below):
    # speculate.go, memoize.go, budget_guard.go, cache_keepalive.go,
    # error_classifier.go, confidence.go, playbook.go, ratchet.go,
    # ratchet_reactive.go, verify_hint.go, tool_output_guard.go,
    # parallel_tools.go, overseer.go, loop_detect.go,
    # repetition_tracker.go, reflection.go, verify.go,
    # autopilot_strategist.go, todo_check.go

  agentruntime/            # Unified runtime layer for desktop, daemon, and TUI
    config_access.go       # Config read/write accessors shared across surfaces
    desktop_adapters.go    # Desktop-specific adapter wiring
    desktop_stream.go      # Desktop streaming event dispatch
    im_round.go            # IM round coordination
    interactions.go        # Shared interaction handling (ask_user, approvals)
    interactive_core.go    # Core interactive session loop
    mobile_interactions.go # Mobile/tunnel-specific interaction routing
    tunnel_interactions.go # Tunnel-specific interaction dispatch
    tunnel_main_stream.go  # Tunnel main stream setup
    types.go               # Shared runtime types

  provider/                # LLM provider implementations
    provider.go            # Provider interface (Name, Chat, ChatStream, CountTokens)
    openai.go              # OpenAI-compatible provider (OpenAI, DeepSeek, Groq, Mistral, etc.)
    anthropic.go           # Anthropic provider
    gemini.go              # Google Gemini provider
    copilot.go             # GitHub Copilot provider (custom transport)
    registry.go            # Provider registry (protocol name → adapter)
    retry.go               # Retry logic with exponential backoff
    adaptive_cap.go        # Adaptive output token cap based on model limits
    context_probe.go       # Context window probing and model capability detection
    model_discovery.go     # Dynamic model list discovery from provider APIs
    impersonate.go         # System prompt impersonation presets
    user_error.go          # User-facing error formatting (rate limits, auth failures)
    vision.go              # Multimodal vision support

  config/                  # Configuration loading and env expansion
    config.go              # Config struct, LoadFromFile, full schema
    config_save.go         # Save, SaveScoped (global/instance), recompact
    config_vendor.go       # Vendor/endpoint CRUD helpers
    config_keys.go         # Config key enumeration and dot-notation access
    config_exposed.go      # Read-only config accessors for WebUI/API
    env.go                 # ${ENV_VAR} expansion
    api_keys.go            # API key resolution, migration, secure storage
    anthropic_bootstrap.go # Anthropic first-run API key bootstrap
    a2a_override.go        # Instance-level A2A config merge from .ggcode/a2a.yaml
    instance.go            # Instance paths, LoadInstanceConfig, SetInstancePaths
    instance_delta.go      # Instance-scoped delta config
    vendor_defaults.go     # Built-in vendor defaults (protocols, base URLs, models)
    knight.go              # Knight-specific config
    onboard.go             # First-run onboarding config
    context_window.go      # Context window size management and overrides
    secure_write.go        # Atomic file write with temp + rename

  context/                 # Conversation context management (imported as `ctxpkg`)
    manager.go             # ContextManager: message history, compression
    tokenizer.go           # CJK-aware token estimation

  cost/                    # Token usage and cost tracking
    manager.go             # CostManager: per-session and total cost
    pricing.go             # Model pricing data
    types.go               # SessionCost / TokenUsage (local type to avoid circular deps)
    tracker.go             # In-flight token counting

  permission/              # Permission and sandbox policy
    mode.go                # PermissionMode enum (supervised/plan/auto/bypass/autopilot)
    policy.go              # PermissionPolicy interface
    config_policy.go       # Config-backed per-tool rules
    dangerous.go           # Dangerous command classification
    sandbox.go             # Path sandbox enforcement

  tool/                    # Built-in tools
    tool.go                # Tool interface (Name, Description, Parameters, Execute)
    builtin.go             # RegisterBuiltinTools: file ops, search, commands, git, web, ask_user, todo_write
    read_file.go           # File reading (text, images, PDF, Office, archives)
    write_file.go          # File writing
    edit_file.go           # Targeted file editing with checkpoint support
    multi_edit_file.go     # Multi-edit single file
    multi_file_edit.go     # Coordinated multi-file edit
    multi_file_read.go     # Batch file reading
    list_dir.go            # Directory listing
    search_files.go        # Regex content search
    glob.go                # Glob pattern file matching
    grep.go                # Ripgrep-based search with context lines
    run_command.go         # Synchronous shell command execution
    command_jobs.go        # Background command job manager and buffered output
    command_job_tools.go   # Async command tools (start/read/wait/write/stop/list)
    web_fetch.go           # HTTP fetch with SSRF protection
    web_search.go          # Web search
    git_diff.go / git_log.go / git_status.go / git_add.go / git_commit.go / git_blame.go  # Git tools
    git_show.go / git_branch_list.go / git_remote.go / git_stash.go / git_stash_list.go  # Git tools
    save_memory.go         # Save memory entries
    todo_write.go          # Write todo lists
    spawn_agent.go         # Spawn sub-agents
    list_agents.go / wait_agent.go  # Sub-agent polling and wait tools
    notebook_edit.go       # Jupyter notebook editing
    sleep.go               # Sleep/delay tool
    worktree.go            # Enter/exit git worktree tools

  tui/                     # Terminal UI (Bubble Tea)
    model.go               # Model struct, Init, configuration methods
    model_update.go        # Update loop: message routing, key handling, agent lifecycle
    model_messages.go      # Message types (streamMsg, doneMsg, errMsg, etc.)
    model_approval.go      # Approval/diff confirmation selection lists
    model_pending.go       # Pending state helpers (device codes, questionnaires)
    model_clipboard.go     # Clipboard image loading
    model_terminal.go      # Terminal utility helpers (open URL, resize)
    view.go                # View rendering, status bar, autocomplete
    commands.go            # Slash command handlers
    submit.go              # Message submission and agent startup
    resize.go              # Window resize handling
    repl.go                # REPL: wires Model to Agent, session, cost, tools
    completion.go          # Slash command autocomplete logic
    viewport.go            # Scrollable viewport with auto-follow
    spinner.go             # Tool execution spinner
    diff.go                # Diff display formatting
    markdown.go            # Markdown rendering with glamour
    preview_panel.go       # File preview, markdown rendering, syntax highlighting
    model_picker.go        # Model selection panel
    provider_picker.go     # Provider/vendor/endpoint selection
    mcp_panel.go           # MCP server management panel
    inspector.go           # Session inspector panel
    harness_panel.go       # Harness workflow panel
    skills_panel.go        # Skills browser panel
    i18n.go                # i18n catalogs (en / zh-CN)
    ask_user.go            # Interactive ask_user questionnaire
    chat_bridge.go         # TUIChatBridge: webchat → TUI event loop
    ask_user.go            # ask_user questionnaire handling
    wechat_panel.go        # WeChat adapter panel
    wecom_panel.go         # WeCom adapter panel
    whatsapp_panel.go      # WhatsApp adapter panel
    extpane/               # External terminal pane management
      manager.go           # PaneManager: lifecycle, maxPanes=10, failed-agent blocklist
      tmux.go              # Tmux backend (priority 1 if $TMUX set)
      kitty.go             # Kitty backend (priority 2, KITTY_WINDOW_ID)
      iterm2.go            # iTerm2 backend (priority 3, TERM_PROGRAM=="iTerm2")
      iterm2_other.go      # iTerm2 no-op stub (non-darwin)
      format.go            # Logfile path formatting
      sizing.go            # Pane sizing helpers (pixel estimation)
      pixel_unix.go        # TIOCGWINSZ pixel size detection (unix)
      pixel_other.go       # Pixel size stub (non-unix)
    cmdpane/               # Command pane management
      manager.go           # CmdPaneManager: terminal pane for running commands

  webui/                   # WebUI HTTP server + WebSocket chat
    server.go              # Server: REST API + WS chat handler
    server_handlers.go     # REST endpoint handlers (config, sessions, MCP, IM, A2A)
    server_static.go       # SPA static file serving
    server_websocket.go    # WebSocket connection lifecycle and event broadcast
    tui_bridge.go          # TUIChatBridge: routes webchat → TUI event loop
    auth.go                # WebUI token authentication
    embed.go               # Embed SPA dist/ files
    dist/                  # Built SPA frontend

  im/                      # IM gateway runtime
    adapters.go            # Adapter interface and registration
    runtime.go             # Manager: multi-adapter routing, bindings, mute/unmute, binding hot watcher
    binding_watcher.go     # Monitors im-bindings.json for LastSessionID changes; auto-mutes adapters when another instance claims ownership
    daemon_bridge.go       # DaemonBridge: agent loop + IM slash commands + ChatBridge impl
    emitter.go             # IMEmitter: outbound event routing
    fanout.go              # Multi-adapter fan-out with per-channel echo suppression
    approval_reply.go      # Tool approval via IM (approve/reject)
    approval_text.go       # Approval message formatting
    ask_user_format.go     # ask_user questionnaire formatting for IM
    ask_user_parse.go      # Parse IM responses back to ask_user answers
    tool_format.go         # Unified tool result formatting for all IM adapters
    markdown_strip.go     # Platform-specific markdown conversion (Signal, Slack, Discord)
    message_split.go       # Per-platform message splitting with size limits
    telegram_adapter.go    # Telegram adapter
    qq_adapter.go          # QQ adapter
    discord_adapter.go     # Discord adapter
    slack_adapter.go       # Slack adapter
    dingtalk_adapter.go    # DingTalk (DingDing) adapter
    feishu_adapter.go      # Feishu (Lark) adapter
    wechat_adapter.go      # WeChat adapter
    wecom_adapter.go       # WeCom (Enterprise WeChat) adapter
    whatsapp_adapter.go    # WhatsApp adapter
    signal_adapter.go      # Signal adapter
    matrix_adapter.go      # Matrix adapter
    mattermost_adapter.go  # Mattermost adapter
    irc_adapter.go         # IRC adapter
    nostr_adapter.go       # Nostr adapter
    twitch_adapter.go      # Twitch adapter
    pc_adapter.go           # PC (webhook-based) adapter
    stt/                   # Speech-to-text support for IM voice messages

  tunnel/                  # Tunnel broker for mobile relay
    broker.go              # Broker: client management, event recording, replay, active session tracking
    relay_client.go        # WebSocket client to relay server with backpressure (30s write deadline)
    session.go             # Tunnel session helpers (SendText, SendSnapshot, etc.)
    protocol.go            # Event types and gateway message structs
    projection_store.go    # Projection state tracking for event ordering
    projection_hash.go     # Projection hash for reconnect verification
    share_protocol.go      # Session share/online/offline protocol
    crypto.go              # End-to-end encryption for tunnel events
    key_exchange.go        # Key exchange for encrypted tunnel sessions
    qrcode.go              # QR code generation for mobile pairing
    relay_url_security.go  # Relay URL validation and security checks
    replay_order.go        # Event replay ordering guarantees

  swarm/                   # Team-based multi-agent coordination
    team.go                # Team creation, deletion, teammate management
    manager.go             # Manager: spawn, list, cancel teammates, CancelAll
    idle_runner.go         # Idle teammate runner: processes inbox tasks

  agentruntime/            # Unified runtime layer (see above)

  a2a/                     # Agent-to-Agent protocol
    server.go              # HTTP server with multi-auth middleware
    client.go              # Auto-negotiating A2A client
    handler.go             # JSON-RPC handler (SendMessage, SendMessageStreaming)
    registry.go            # Local instance registry (PID-based discovery)
    mcp_bridge.go          # MCP bridge: exposes remote agents as MCP tools
    remote_tool.go         # Remote tool executor
    types.go               # Shared types (Task, Artifact, AgentCard, auth config)
    mdns.go                # mDNS LAN discovery (broadcast and listen)
    ip.go                  # Local IP address discovery

  acp/                     # Agent Client Protocol (ACP)
    handler.go             # ACP handler: expose ggcode as an ACP agent for JetBrains, Zed, etc.
    client.go              # ACP client: connect to external ACP-compatible agents
    client_manager.go      # Multi-client ACP session management
    agent_loop.go          # ACP agent loop integration
    adapter.go             # ACP tool adapter
    transport.go           # ACP transport layer (stdio, WebSocket)
    session.go             # ACP session lifecycle
    discovery.go           # ACP agent discovery
    auth.go                # ACP authentication
    activity_trail.go      # Activity trail for ACP sessions
    output_tail.go         # Output tailing for ACP
    mcp_bridge.go          # MCP bridge for ACP tools
    types.go               # ACP types and protocol definitions

  acpclient/               # ACP client utilities
    manager.go             # Manager for spawning and tracking ACP-compatible editor agents (Claude, Codex, etc.)

  auth/                    # Full authentication subsystem
    store.go               # Credential store
    copilot.go             # GitHub Copilot token management
    pkce.go                # PKCE helpers (code verifier/challenge generation)
    claude_oauth.go        # Claude OAuth flow
    a2a_oauth.go           # A2A OAuth2 PKCE/Device Flow providers
    a2a_presets.go         # Provider presets (GitHub, Google, Auth0, Azure)
    a2a_token_cache.go     # Token cache with per-{provider}-{clientID} isolation

  checkpoint/              # In-memory file checkpointing
    checkpoint.go          # File edit checkpoints for undo/revert support

  commands/                # Markdown-backed skills and slash commands
    command.go             # Command/skill metadata and expansion helpers
    loader.go              # Load skills and legacy commands from ~/.agents, ~/.ggcode, and project .ggcode

  cron/                    # Scheduled job management
    parser.go              # Cron expression parsing (5-field standard format)
    scheduler.go           # Scheduler: recurring jobs (persisted) and one-shot reminders (in-memory)

  daemon/                  # Daemon mode
    daemon.go              # Daemon struct, keyboard shortcuts, i18n labels, follow display

  debug/                   # Debug logging
    debug.go               # Debug log helpers (file-based, toggled by env var)

  diff/                    # Diff formatting
    diff.go                # FormatDiff, IsDiffContent

  extract/                 # Content extraction
    extract.go             # File content extraction utilities

  hooks/                   # Pre/post execution hooks
    hook.go                # Hook struct
    runner.go              # HookRunner: executes pre/post tool hooks

  harness/                 # Harness control plane
    config.go              # Harness config model and defaults
    project.go             # Project discovery and scaffold creation
    check.go               # Structural checks and validation commands
    run.go                 # Tracked harness runs / queued execution
    release.go             # Release-batch planning and persisted release reports
    worktree.go            # Git worktree lifecycle for isolated task workspaces
    gc.go                  # Archive/prune stale harness state
    auto_init.go           # Auto-init: detect projects and scaffold
    auto_run.go            # Auto-run: pollable sub-agent-backed workers
    templates.go           # Harness prompt templates
    worker.go              # Worker: sub-agent-backed task execution
    context.go             # Bounded context binding
    context_config.go      # Context configuration model

  image/                   # Image handling for multimodal input
    image.go               # ReadFile, Placeholder
    clipboard_*.go         # Platform-specific clipboard integration (darwin, linux, windows)

  install/                 # Self-update and install
    install.go             # Self-update logic (download, verify, replace binary)

  knight/                  # Knight: background autonomous agent
    analyzer.go            # Code change analysis and pattern extraction
    budget.go              # Daily token budget management
    budget_buckets.go     # Budget bucket allocation per category
    auto_policy.go         # Auto-policy decision engine
    auto_promote_eval_log.go  # Auto-promotion evaluation logging
    usage_tracker.go       # Usage tracking and reporting
    ab_replay.go           # A/B replay for skill evaluation
    skill_promoter.go      # Skill promotion pipeline
    skill_validator.go     # Skill validation
    skill_scenario_log.go  # Skill scenario logging
    candidate_name.go      # Candidate name generation

  lsp/                     # LSP client integration
    client.go              # Generic LSP client (gopls, rust-analyzer, clangd, etc.)
    discovery.go           # Auto-discovery from PATH and workspace files
    operations.go          # LSP operations (definition, references, hover, rename, etc.)
    session.go             # LSP session lifecycle management

  markdown/                # Markdown rendering
    render.go              # Glamour-based markdown rendering helpers

  mcp/                     # Model Context Protocol client
    client.go              # MCPClient: spawn and communicate with MCP servers
    adapter.go             # Tool adapter (MCP tool → ggcode tool interface)
    jsonrpc.go             # JSON-RPC protocol implementation
    oauth.go               # OAuth 2.1: metadata discovery, DCR, device flow, token refresh
    install.go             # MCP server installation (npm, npx, etc.)
    migration.go           # MCP config migration from legacy formats
    presets.go             # MCP preset definitions

  memory/                  # Project and auto memory
    auto.go                # AutoMemory: automatic memory extraction from sessions
    project.go             # ProjectMemory: load GGCODE.md, AGENTS.md, CLAUDE.md, COPILOT.md

  metrics/                 # Token usage metrics
    collector.go           # Metrics collector: aggregates usage across sessions
    digest.go              # Periodic usage digest generation
    summary.go             # Human-readable usage summary
    metrics.go             # Metrics type definitions

  plugin/                  # Go plugin system
    loader.go              # Plugin loader
    mcp_loader.go          # MCP server plugin loader
    plugin.go              # Plugin interface

  relaycatalog/            # Relay catalog
    client.go              # Client for relay model catalog API
    manager.go             # Catalog manager: model list, capability discovery
    store.go               # Catalog persistence
    layout_infer.go        # Layout inference from catalog data

  restart/                 # Process restart
    restart.go             # Restart support (exec self with same args)

  safego/                  # Safe goroutine helpers
    safego.go              # Panic recovery wrappers for goroutines (GoSafe, GoSafeWait)

  session/                 # Session persistence
    store.go               # Store: save/load sessions as JSONL with tunnel event recording.
                           #   Meta record persists: permission_mode, sidebar_visible (*bool),
                           #   title, workspace, vendor, endpoint, model, token usage.
                           #   Checkpoint support for summarize compaction.
    lock.go                # Session file locking (cross-platform)
    lock_unix.go           # Unix flock-based locking
    lock_windows.go        # Windows LockFileEx-based locking
    endpoint_stats.go      # Per-endpoint usage statistics

  stream/                  # Stream processing
    stream.go              # Stream utilities (channel helpers, fan-out)

  subagent/                # Sub-agent spawning and management
    manager.go             # Manager: spawn, list, cancel, snapshot sub-agents (semaphore concurrency)
    runner.go              # Runner: execute sub-agent tasks with timeout (default 30 min)

  task/                    # Task tracking primitives
    task.go                # Task primitives (create, update, dependencies, blocks/blockedBy)

  tmux/                    # Tmux client utilities
    client.go              # Tmux command wrapper for pane/window management

  runfile/               # Port file management for external process discovery
    runfile.go             # Write/read ~/.ggcode/run/<sessionID>.json

  vcs/                   # Multi-VCS abstraction (auto-detect git/hg/svn/jj)
    vcs.go                 # VCS interface and auto-detection
    git.go                 # Git backend

  lanchat/               # LAN Chat P2P messaging between ggcode instances
    hub.go                 # Hub: message routing, presence, identity
    types.go               # Core types (Message, Participant, Nickname)
    handlers.go            # Message handlers
    udp_transport.go       # TCP/UDP transport with fallback (mDNS via transport)
    store.go               # Message persistence (session JSONL)
    nicknames.go           # Nickname management
    attachment.go          # File attachment support
    peers_prompt.go        # Peer list prompt generation

  uiusage/                 # UI usage display
    display.go             # Token usage display formatting for UI surfaces

  util/                    # Shared utilities
    truncate.go            # String truncation, shell quoting

  version/                 # Build-time version info
    version.go             # Version, Commit, Date (injected via -X ldflags)

  chat/                    # Chat utilities
    types.go               # Shared chat types and helpers

docs/                      # Documentation
  ARCHITECTURE.md          # This file
  a2a-auth.md              # A2A authentication guide (5 schemes, config examples)
  releases/                # Version-specific release notes
  design/                  # Design documents and architecture decisions
  guide/                   # User guides
  reviews/                 # Review reports (security, mobile, full-codebase)

config/                    # MCP preset configuration (mcporter.json)

mobile/                    # Mobile application (Flutter)
  flutter/                 # Flutter app source
    lib/                   # Dart source: session provider, tunnel protocol, UI
    ios/                   # iOS platform code + fastlane
    android/               # Android platform code + fastlane

npm/                       # npm wrapper package (installs GitHub Release binary)
python/                    # Python wrapper (PyPI: ggcode)

scripts/                   # Build, release, and development scripts
  dev/                     # Development helpers (verify-ci.sh)
  release/                 # Release automation (winget, scoop, etc.)
```

## Build Tags

All Go operations require the `goolm` build tag (set in Makefile via `TAGS := goolm`). Without it, builds fail due to missing libolm C headers (mautrix crypto dependency for tunnel encryption).

```bash
go build -tags goolm ./...
go vet -tags goolm ./...
go test -tags "goolm,!integration" ./...     # unit tests (CI-equivalent)
go test -tags "goolm,integration" ./...      # include integration tests
```

CI alignment: `scripts/dev/verify-ci.sh` mirrors the CI pipeline and clears provider env vars before running tests.

## Key Patterns

- **Bubble Tea streaming**: Agent runs in a goroutine; events flow into the TUI via `tea.Program.Send()`
- **Permission policy**: Two layers — tool-level `ShouldAsk` + dangerous-command detection
- **Import cycle avoidance**: Shared types defined in downstream packages; `internal/context` imported as `ctxpkg`; `cost.TokenUsage` defined locally to avoid importing `provider`
- **Platform-specific files**: Go build tags for OS-specific code (`clipboard_darwin.go`, `clipboard_linux.go`, `clipboard_windows.go`, `run_command_unix.go` with `//go:build unix`, `run_command_other.go` with `//go:build !unix`)
- **MCP client**: Spawns fresh process per tool call (`callToolStandalone`); HTTP transport supports OAuth 2.1 with automatic metadata discovery, dynamic client registration, device flow, and token refresh
- **Provider SDKs**: OpenAI (go-openai), Anthropic (anthropic-sdk-go), Gemini (genai), Copilot (custom transport on top of the provider abstraction)
- **IM routing**: IM events are fanned out to all bound adapters; per-channel echo suppression skips the originating adapter for user mirror messages
- **Session format**: JSONL with index.json metadata; checkpoints recorded inline after compaction
- **A2A multi-auth**: Server advertises enabled auth schemes in agent card; client auto-negotiates the strongest available. Auth middleware validates each scheme independently. Multiple schemes can coexist.
- **Token cache**: OAuth2/OIDC tokens cached at `~/.ggcode/oauth-tokens/{provider}-{clientID[:12]}.json` with per-client isolation. Same client_id = shared token; different client_id = isolated.
- **IM mute**: In-memory only (not persisted to binding store). `MuteAllExcept(adapter)` prevents self-mute race. Daemon restart recovers all adapters.
- **WebUI ChatBridge**: Decouples WebSocket chat from agent implementation via `ChatBridge` interface (`SendUserMessage`, `Messages`, `Subscribe`). Two implementations:
  - `DaemonBridge` (daemon mode): Injects webchat messages through `pendingInterruptions` into agent's `SetInterruptionHandler`. Broadcasts events from agent stream callback.
  - `TUIChatBridge` (TUI mode): Routes webchat messages through `program.Send(webchatUserMsg)` into bubbletea event loop. No direct agent access — TUI handles queuing/interruption identically to keyboard input.
- **Tunnel event persistence**: Tunnel events are appended to session JSONL via `AppendTunnelEventToDisk()` without rewriting the whole file. On reconnect, `replayCanonicalEvents()` replays recorded events. `TunnelEventsComplete` flag ensures only fully-recorded event sets are used for replay; incomplete sets fall back to snapshot-based recovery.
- **Relay backpressure**: Peer writes in `ggcode-relay` use blocking sends with a 30s write deadline instead of buffered channel drops, preventing silent data loss during slow connections.
- **Relay event dedup**: `room.upsertHistoryEvent()` deduplicates by sessionID+eventID so replayed events don't accumulate. `snapshot_reset` (empty eventID) is not persisted to SQLite.
- **Swarm task board**: Tasks are assigned to specific teammates via `swarm_task_create` with `assignee`, which pushes directly to the assignee's inbox. Unassigned tasks can be claimed by any idle teammate. Task completion is tracked on a shared board visible to all teammates.
- **Extpane backend detection**: When terminal environments nest (e.g. iTerm2 inside tmux), tmux wins because `$TMUX` is checked first. Each backend captures its own window ID at init to avoid self-closure. `maxPanes=10` hard cap; `failed[agentID]` permanent blocklist after first failure.
- **Extpane tmux hook suppression**: User tmux configs with `set-hook -g after-new-window 'command-prompt ...'` would block tab creation. The tmux backend temporarily suppresses this hook before `new-window`, then restores it.
- **TunnelHost unified management**: `agentruntime.TunnelHost` manages tunnel event streaming for all three surfaces (TUI, Daemon, Wails). Stream callbacks route through `PushStreamEvent` → broker → event recorder (persist) → session store → online broker (forward to mobile).
- **Interrupt/exit cascading**: ctrl+c/esc calls `cancelActiveRun()` which also calls `subAgentMgr.CancelAll()` and `swarmMgr.CancelAll()`, cancelling all running sub-agents and swarm teammates on interrupt.
- **Config validation**: Legacy `provider`/`providers` keys are explicitly rejected at load time; only `vendor`/`endpoint`/`vendors` schema is supported.

## Desktop Architecture (Wails)

The desktop application uses Wails v2 to bridge a Go backend with a React/TypeScript frontend. It is a separate Go module under `desktop/`.

```
desktop/
  ggcode-desktop-wails/        # Wails app (separate go.mod)
    app.go                     # All Wails-bound methods (chat, config, IM, MCP, LSP, tunnel, sessions)
    frontend/                  # React SPA (Vite + TypeScript)
      src/
        App.tsx                # Root component
        components/            # ChatView, Sidebar, Settings, ModelPicker, Inspector, etc.
        i18n/                  # en.json, zh-CN.json
    wails.json                 # Wails build config

  wailskit/                    # Shared Go backend (imported by ggcode-desktop-wails)
    chat.go                    # ChatBridge: agent lifecycle, streaming events, tool calls
    config.go                  # Config REST surface for desktop settings UI
    sessions.go                # Session management (list, load, delete, workspace filter)
    im.go                      # IM adapter lifecycle (enable/disable/mute/bind/unbind)
    mcp.go                     # MCP server management
    lsp.go                     # LSP discovery, status, install
    files.go                   # File serving for preview
    logstream.go               # Log streaming
```

### Key design decisions

- **Event-driven rendering**: The frontend receives streaming events (`text_delta`, `tool_call`, `tool_result`, `done`, `run_done`, etc.) via Wails event emitter. No polling — each event triggers a targeted React state update.
- **TunnelHost integration**: The Wails backend uses `agentruntime.TunnelHost` for unified tunnel event management, identical to the TUI and daemon.
- **Tool approval dialogs**: Desktop intercepts `ask_user` events and renders native-style approval dialogs instead of terminal lists.
- **Session workspace filtering**: `ListSessions()` filters by working directory so users only see sessions for the current project.
- **LSP integration**: Desktop Settings > Integrations > Language Servers shows auto-detected servers with one-click install (scope: user > global > project).

## Tunnel & Relay Architecture

The tunnel subsystem enables mobile and remote interaction with running ggcode sessions.

```
  ┌──────────────────────────────────────────────────────┐
  │  Host (TUI / Daemon / Desktop)                        │
  │                                                       │
  │  Agent ──► agentruntime.TunnelHost                    │
  │              │                                        │
  │              ▼                                        │
  │           tunnel.Broker                               │
  │           ├── Event recording (→ session JSONL)       │
  │           ├── Replay on reconnect                     │
  │           ├── Active session tracking                 │
  │           └── Multi-session switching                 │
  │              │                                        │
  │              ▼                                        │
  │           tunnel.RelayClient ──── WebSocket ──────►  │
  └──────────────────────────────────────────────────────┘
                                              │
                                    ┌─────────┴──────────┐
                                    │  ggcode-relay       │
                                    │  (standalone binary) │
                                    │                     │
                                    │  Room per workspace  │
                                    │  SQLite persistence  │
                                    │  Event dedup by      │
                                    │   sessionID+eventID  │
                                    │  active_session      │
                                    │   binding            │
                                    └─────────┬──────────┘
                                              │
                                    ┌─────────┴──────────┐
                                    │  Mobile Client      │
                                    │  (Flutter app)      │
                                    │                     │
                                    │  Connects to relay   │
                                    │  Receives events     │
                                    │  Sends interactions  │
                                    └────────────────────┘
```

### Key properties

- **Event persistence**: Tunnel events are appended to session JSONL via `AppendTunnelEventToDisk()`. On reconnect, `replayCanonicalEvents()` replays the canonical event stream.
- **Completeness flag**: `Session.TunnelEventsComplete` ensures only fully-recorded event sets are used for replay. Incomplete sets fall back to snapshot-based recovery.
- **Relay backpressure**: Peer writes use blocking sends with a 30s write deadline instead of channel drops, preventing silent data loss.
- **Event dedup**: Relay's `room.upsertHistoryEvent()` deduplicates by sessionID+eventID. `snapshot_reset` (empty eventID) is not persisted.
- **E2E encryption**: Tunnel supports encrypted event delivery via key exchange (`key_exchange.go`, `crypto.go`).
- **QR code pairing**: Mobile clients pair via QR code (`qrcode.go`) that encodes relay URL + session token.

## A2A Authentication Architecture

```
┌──────────────────────────────────────────────────────────┐
│                    A2A Server                             │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ Auth Middleware                                      │ │
│  │  ┌─────────┐ ┌──────────┐ ┌─────┐ ┌──────┐ ┌────┐ │ │
│  │  │ API Key │ │ OAuth2   │ │OIDC │ │ mTLS │ │No  │ │ │
│  │  │ X-Header│ │ Bearer   │ │JWT  │ │ Cert │ │Auth│ │ │
│  │  └────┬────┘ └────┬─────┘ └──┬──┘ └───┬──┘ └─┬──┘ │ │
│  │       └───────┬───┴──────────┴────────┴───────┘    │ │
│  │               ▼ pass / fail                         │ │
│  │         [Request Context: identity]                 │ │
│  └─────────────────────────────────────────────────────┘ │
│  Agent Card: { schemes: [apiKey, oauth2, oidc, mtls] }   │
└──────────────────────────────────────────────────────────┘
         ▲                    ▲
    HTTP with             TLS with
    X-API-Key             client cert
         │                    │
┌────────┴──────┐    ┌───────┴─────┐
│  ggcode #1    │    │  ggcode #2  │
│  (client)     │    │  (client)   │
│               │    │             │
│ Token Cache:  │    │ Token Cache:│
│ ~/.ggcode/    │    │ ~/.ggcode/  │
│  oauth-tokens/│    │  oauth-     │
│   github-xxx  │    │   tokens/   │
└───────────────┘    └─────────────┘
```

Server rebuilds auth state from config on restart (no persistence needed). Client tokens survive restarts via cache. Optional mDNS broadcast (`a2a.lan_discovery`) enables LAN peer discovery when auth is configured.

See [`docs/a2a-auth.md`](a2a-auth.md) for the full authentication guide.

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

### Provider Error Detection

All provider adapters detect output truncation and policy errors:
- **OpenAI**: `finish_reason=length` returns error
- **Anthropic**: `stop_reason=max_tokens` or `stop_reason=refusal` returns error
- **Gemini**: `FinishReason=MAX_TOKENS`, `SAFETY`, `RECITATION`, etc. returns error

Default `max_output_tokens` is 16384 (configurable per endpoint). Adaptive cap (`adaptive_cap.go`) adjusts based on model-specific limits.

## Agent Optimization Stack

The agent loop (`internal/agent/`) includes multiple research-inspired optimization layers that reduce token waste, detect pathological patterns, and improve task success rates. All are deterministic (no LLM cost) and run in-process.

| Layer | File | Inspired By | Purpose |
|-------|------|-------------|---------|
| Speculative execution | `speculate.go` | PASTE (arXiv:2603.18897) | Pre-execute likely next read-only tools during LLM generation |
| Tool memoization | `memoize.go` | ToolCaching (arXiv:2601.15335) | Cache read-only tool results with mtime/TTL invalidation |
| Parallel execution | `parallel_tools.go` | LLMCompiler (ICML 2024) | Concurrent read-only tools per LLM batch (max 3) |
| Tool-result clearing | `agent_precompact.go` | Anthropic context engineering | Replace old tool_result outputs with placeholders |
| Tool-use input clearing | `agent_precompact.go` | Anthropic context engineering | Truncate old edit/write Input args after result cleared |
| Superseded reads | `manager.go` (context) | Headroom | Compact stale re-reads of same file |
| Tool output guard | `tool_output_guard.go` | Chroma context fill study | Progressive truncation by context fill % |
| Budget guard | `budget_guard.go` | BAGEN (arXiv:2606.00198) | Per-step token cost trend monitoring |
| Error classifier | `error_classifier.go` | AgentDebug (arXiv:2509.25370) | 10-category error-specific guidance on first error |
| Confidence scorer | `confidence.go` | HTC (arXiv:2601.15778) | Holistic 6-signal trajectory quality metric |
| Error streak | `loop_detect.go` | SICA (arXiv:2504.15228) | Progressive guidance at 4/7/10 consecutive errors |
| Drift detection | `overseer.go` | SICA | Progressive drift at 20/40/60 iterations |
| Repetition tracker | `repetition_tracker.go` | SICA | Detect failed-edit clusters on same file |
| Ratchet rules | `ratchet.go`, `ratchet_reactive.go` | SICA | Learn error patterns from failures, inject preventively |
| Strategy playbook | `playbook.go` | ACE (ICLR 2026) | Learn successful tool patterns, inject as hints |
| Smart verify hint | `verify_hint.go` | Generate-Verify-Fix loop | Post-edit build reminders with smart reset |
| Constraint pinning | `manager.go` (context) | Governance Decay (arXiv:2606.22528) | Preserve user constraints across compaction |
| Reasoning block compaction | `manager.go` (context) | Anthropic thinking API docs | Clear old reasoning blocks (keep N=3) |
| Fallback checkpoint | `agent_compact.go` | — | Force checkpoint when messages > 500 even if compaction fails |
| Prompt cache keepalive | `cache_keepalive.go` | Aider pattern | Ping provider every 270s during idle to keep cache warm (Anthropic only) |
| Token calibration | `token_calibrator.go` (context) | — | Self-calibrating char/token ratio using API feedback |
| MCP read-only mode | `readonly.go` (mcp) | Devin enterprise | Per-server read_only flag blocks write-type tool calls |

### Context Window Management Pipeline

When context approaches the compaction threshold, a multi-stage pipeline activates:

1. **Superseded reads compaction** — replace stale re-reads of same file (safest, mechanical)
2. **Tool-result clearing tiers** — at 50%/65%/75% of threshold, replace old tool_result outputs
3. **Tool-use input clearing** — truncate old edit/write Input args matching cleared results
4. **Reasoning block compaction** — clear old thinking/reasoning_content from assistant turns
5. **Precompact (background)** — LLM-based summarization in background goroutine
6. **Reactive compact (synchronous)** — if precompact fails, truncation as fallback

## WebUI Architecture

The WebUI subsystem provides an HTTP+WebSocket interface for browser-based interaction with ggcode. It starts in both TUI and daemon modes on `127.0.0.1:0` (random port). In TUI mode, the URL is displayed as a system message inside the chat area.

### ChatBridge Interface

```
                    ┌──────────────────────────┐
                    │     webui.Server          │
                    │  ┌──────────────────────┐ │
                    │  │ REST API              │ │
                    │  │ /api/config, sessions │ │
                    │  │ /api/mcp, im, a2a...  │ │
                    │  └──────────────────────┘ │
                    │  ┌──────────────────────┐ │
                    │  │ WebSocket Chat        │ │
                    │  │  ↓ SendUserMessage()  │ │
                    │  │  ↑ Subscribe() events │ │
                    │  └──────┬───────────────┘ │
                    └─────────┼─────────────────┘
                              │ ChatBridge interface
                    ┌─────────┼─────────────────┐
                    │         ▼                  │
          ┌─────────┴──────────┐  ┌─────────────┴──────────┐
          │   DaemonBridge     │  │    TUIChatBridge        │
          │   (daemon mode)    │  │    (TUI mode)           │
          │                    │  │                         │
          │ pendingInterrupts  │  │ program.Send()          │
          │ → agent interrupt  │  │ → bubbletea event loop  │
          │                    │  │ → startAgent /           │
          │ broadcastEvent()   │  │   queuePendingSubmission │
          │ from agent stream  │  │                         │
          │ callback           │  │ BroadcastEvent()        │
          │                    │  │ from TUI stream callback │
          └────────────────────┘  └─────────────────────────┘
```

### REST API Endpoints

| Path | Methods | Description |
|------|---------|-------------|
| `/api/config` | GET | Full configuration (vendors, endpoints, MCP, IM, A2A, general) |
| `/api/config/active` | GET/PUT | Active vendor/endpoint/model selection |
| `/api/vendors` | GET/POST | Vendor list / add vendor |
| `/api/vendors/{id}` | GET/PUT/DELETE | Vendor CRUD |
| `/api/vendors/{id}/endpoints` | GET/POST | Endpoint list / add |
| `/api/vendors/{id}/endpoints/{ep}` | GET/PUT/DELETE | Endpoint CRUD |
| `/api/vendors/{id}/endpoints/{ep}/apikey` | PUT | Set API key |
| `/api/mcp` | GET/POST | MCP servers config / add |
| `/api/mcp/status` | GET | Runtime MCP status |
| `/api/mcp/{name}` | DELETE | Remove MCP server |
| `/api/im` | GET | IM config |
| `/api/im/status` | GET | Runtime IM adapter status |
| `/api/im/adapters/{name}` | POST | IM adapter action (enable/disable/mute) |
| `/api/general` | GET/PUT | General settings (language, mode, iterations) |
| `/api/impersonate` | GET/PUT | Impersonation preset selection |
| `/api/a2a` | GET/PUT | A2A config |
| `/api/a2a/discover` | GET | Discover remote agents |
| `/api/sessions` | GET | List sessions grouped by workspace |
| `/api/sessions/{id}` | GET/DELETE | Session detail / delete |
| `/api/chat/history` | GET | Current chat history (from agent) |
| `/api/chat/ws` | WS | WebSocket chat (send messages, receive streaming events) |
| `/api/restart` | POST | Trigger restart |

### WebSocket Chat Protocol

**Client → Server** (JSON):
```json
{"type": "user_message", "text": "explain goroutines", "images": [...], "files": [...]}
```

**Server → Client** (JSON, event types):
| Event | Fields | Description |
|-------|--------|-------------|
| `user_ack` | `text`, `image_count`, `file_names` | Confirms message received |
| `text_delta` | `text` | Streaming text chunk |
| `tool_call_chunk` | `id`, `name`, `arguments_delta` | Partial tool call |
| `tool_call` | `id`, `name`, `arguments` | Complete tool call |
| `tool_result` | `name`, `result` | Tool execution result |
| `error` | `error` | Agent error |
| `done` | `usage` | Stream complete with token usage |

### Concurrency Safety

1. **WebSocket**: Per-connection write goroutine with buffered channel (256). Read/write fully separated.
2. **DaemonBridge.SendUserMessage**: TOCTOU-safe — cancelFunc check and run-slot claim happen under a single mutex lock.
3. **TUIChatBridge**: No direct agent access. Messages route through bubbletea event loop (`program.Send`), identical to keyboard input.
4. **Broadcast**: Non-blocking sends to subscriber channels. Slow subscribers drop events instead of blocking.
