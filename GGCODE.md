> AI coding agent for the terminal. Go codebase with Bubble Tea TUI, multi-provider LLM support, MCP integration, and harness-engineering workflows.

## Quick Reference

| Item | Value |
|------|-------|
| Module | `github.com/topcheer/ggcode` |
| Go version | 1.26.2 |
| Build tags | **`goolm`** — required for all `go build`, `go vet`, `go test` commands |
| CLI framework | Cobra (`spf13/cobra`) |
| TUI framework | Bubble Tea / Lip Gloss (`charmbracelet/bubbletea`, `charmbracelet/lipgloss`) |
| Storage | JSON files — harness uses JSON events/snapshots; sessions use JSONL files |
| License | MIT |
| Build output | `bin/ggcode` |
| Latest documented release | [`v1.3.108`](docs/releases/v1.3.108.md) |

## Build & Validation

**All Go commands require the `goolm` build tag** (set in Makefile via `TAGS := goolm`).
Without it, builds fail due to missing libolm C headers (mautrix crypto dependency).

```bash
make build          # go build -tags goolm -o bin/ggcode ./cmd/ggcode
make build-desktop-wails  # wails build -tags goolm (Wails desktop app)
make test           # go test -tags goolm ./...
make lint           # go vet -tags goolm ./...
make install        # go install -tags goolm github.com/topcheer/ggcode/cmd/ggcode
make clean          # rm -rf bin/
```

If running `go` commands directly (not via `make`), always add `-tags goolm`:
```bash
go build -tags goolm ./...
go vet -tags goolm ./...
go test -tags goolm ./...
go test -tags "goolm,integration" ./...   # integration tests
```

CI (`.github/workflows/ci.yml`):
- `CGO_ENABLED=0 go build -tags goolm -o /tmp/ggcode ./cmd/ggcode`
- `go vet -tags goolm ./...`
- `go test -tags "goolm,!integration" ./...`
- `gofmt -l .` must produce no output (separate `lint` job)

Local CI-aligned verification lives in `scripts/dev/verify-ci.sh`; it mirrors the same build/vet/test
chain and also clears provider integration-test env vars before running tests.

Linter config (`.golangci.yml`): `gofmt`, `govet`, `errcheck`, `staticcheck`, `unused`. Test files excluded from `errcheck`/`govet`.

## Project Layout

```
cmd/ggcode/            CLI entrypoint, root command, pipe mode, resume, harness/mcp subcommands
cmd/ggcode-installer/  Standalone Go installer that downloads release binaries
desktop/               Desktop GUI application (Wails-based)
  ggcode-desktop-wails/ Wails desktop app — React frontend, Go backend
  wailskit/            Shared Go backend for Wails desktop (ChatBridge, config, tunnel)
ggcode-relay/          Standalone relay server for mobile tunnel (WebSocket, SQLite event persistence)
internal/              488 Go source files (~149k LOC non-test, ~120k LOC test)
  agent/               Core agent loop, tool execution, autopilot, compaction, memory (agent.go + split files)
  provider/            LLM provider adapters: OpenAI, Anthropic, Gemini, Copilot + retry logic
  webui/               WebUI HTTP server + WebSocket chat, SPA, config/session REST API, ChatBridge interface
  im/                  IM gateway runtime, QQ/Telegram/Discord/Slack/DingTalk/Feishu adapters, pairing, channel bindings, per-channel echo suppression, outbound routing, daemon bridge with slash commands (/listim, /muteim, /muteall, /muteself, /restart)
  tunnel/              Tunnel broker for mobile relay: event persistence to JSONL, replay on reconnect, active session tracking, relay client with backpressure, multi-session switching
  swarm/               Team-based multi-agent coordination: teammates with own agent loop and inbox, shared task board, assignee-based delivery
  daemon/              Daemon mode: follow display, background forking, session picker, i18n labels
  tui/                 Bubble Tea TUI: views, panels, slash commands, i18n (en/zh-CN), fullscreen file browser + preview, extpane terminal tabs for sub-agents/teammates
    extpane/           External terminal pane management: tmux/iTerm2/Kitty backends, tab lifecycle, self-close safety
  tool/                Built-in tools (file ops, search, commands, git, web, agents, productivity)
  harness/             Harness-engineering workflow engine (task management, worktrees, review, release)
  mcp/                 MCP client: JSON-RPC, process management, install, migration, presets, OAuth 2.1 auth
  config/              YAML config loading, env expansion, API key handling, Anthropic bootstrap, A2A auth config (api_key, oauth2, oidc, mtls)
  memory/              Project memory loading (GGCODE.md, AGENTS.md, etc.) + auto-memory persistence
  subagent/            Sub-agent spawning, tracking, coordination (manager + runner)
  knight/              Knight background agent: autonomous code monitoring, daily token budget
  a2a/                 Agent-to-Agent protocol: server (multi-auth), client (auto-negotiate), registry, MCP bridge, E2E mesh test
  acp/                 Agent Client Protocol support (JetBrains, Zed, ACP-compatible editors)
  lsp/                 LSP client integration (gopls, rust-analyzer, clangd, etc.) with auto-discovery, config overrides, and scoped install (user/global/project)
  commands/            Slash command registry (bundled + loaded), usage formatting, skill templates
  context/             Conversation context window management and tokenization (imported as `ctxpkg`)
  session/             JSONL-backed session persistence with tunnel event recording
  cron/                Scheduled job management (cron expressions, one-shot reminders)
  checkpoint/          In-memory file checkpointing for undo/revert support
  permission/          Permission modes + per-tool policy enforcement + sandbox + dangerous tool classification
  plugin/              External tool plugins (command-based, MCP-based)
  hooks/               Pre/post hooks runner (5 events: agent start/stop, stream start/stop, tool use; command + HTTP types; HMAC-signed JSON payload)
  cost/                Token usage tracking + billing-type detection (per-token, subscription/coding plan, free; endpoint-based coding plan lookup)
  auth/                Full auth stack: GitHub Copilot token mgmt, OAuth2 PKCE/Device Flow, OIDC Discovery, JWT validation (HS256/RS256/ECDSA), JWKS polling, token introspection, token cache with per-client isolation
  chat/                Chat utilities and shared types
  markdown/            Markdown rendering helpers
  extract/             Content extraction utilities
  stream/              Stream processing utilities
  task/                Task tracking primitives
  safego/              Safe goroutine helpers with panic recovery
  restart/             Process restart support
  runfile/             Port file management (~/.ggcode/run/<sessionID>.json) for external process discovery
  image/               Image processing, clipboard integration (platform-specific: darwin, linux, windows)
  install/             Self-update and install logic
  update/              Version checking and auto-update
  debug/               Debug logging utilities
  diff/                Diff formatting
  version/             Build-time version/commit/date (injected via ldflags)
  util/                Shell quoting, text truncation
docs/                  Architecture docs, design docs, release notes, A2A auth guide, user guides, site content
npm/                   npm wrapper package (installs GitHub Release binary)
python/                Python wrapper (PyPI: ggcode)
scripts/               Release scripts, site scripts
config/                MCP preset configuration (mcporter.json)
```

## Architecture

- **Agent loop** (`internal/agent/`): Central loop sends user messages to the LLM, executes tool calls, feeds results back. Split into focused files: `agent.go` (core struct, Run/RunStream), `agent_autopilot.go` (continuation + Goal-directed execution), `agent_compact.go` (auto-compaction), `agent_memory.go` (memory helpers), `agent_tool.go` (tool execution, diff confirm, hooks).
- **Provider adapters** (`internal/provider/`): Each LLM provider (OpenAI, Anthropic, Gemini, Copilot) has a protocol-specific adapter. `registry.go` maps protocol names to adapters via `NewProvider()`. Supported protocols: `openai`, `anthropic`, `gemini`, `copilot`. All implement the `Provider` interface (Name, Chat, ChatStream, CountTokens). Retry logic handles transient failures.
- **Permission modes** (`internal/permission/mode.go`): Five modes in a cycle: `supervised → plan → auto → bypass → autopilot`. Each mode defines default tool allow/deny rules. Autopilot auto-escalates blocked states to `ask_user`. Dangerous tools are classified in `dangerous.go`. **Mode is session-scoped**: switching mode saves to `session.PermissionMode` (persisted in JSONL meta record), not to global config. New sessions default to `cfg.DefaultMode` (or `supervised` if unset). Resuming a session restores its saved mode.
- **Harness** (`internal/harness/`): Multi-step engineering workflow engine with task queues, dependency tracking, git worktrees, context management, drift detection, inbox, promotion, review, release automation, and a monitor. Uses JSON files for event/snapshot storage.
- **IM runtime** (`internal/im/`): Workspace-bound IM routing with multi-adapter support (QQ, Telegram, Discord, Slack, DingTalk, Feishu, WeChat, IRC, Matrix, Nostr, Signal, WhatsApp, Mattermost, Twitch). Handles pairing, persisted bindings, per-channel echo suppression, and mirrored outbound delivery for remote chat surfaces. Configurable output modes (verbose/quiet/summary) control tool result granularity. Daemon bridge provides IM slash commands for adapter management (`/listim`, `/muteim <name>`, `/muteall`, `/muteself`, `/restart`, `/help`). The `im` tool (`internal/tool/im_tool.go`) lets the LLM manage adapters and send messages; it bridges to `im.Manager` via `im.ToolManagerAdapter`.
  - **mute vs disable**: Both drop the connection (cancel context + delete sink). `mute` keeps the binding in `currentBindings` (UI shows it as muted); `disable` moves it to `disabledBindings`. `unmute`/`enable` reconnects via `onRestart` callback.
  - **Multi-instance conflict**: `InstanceDetect` uses PID files under `.ggcode/instances/` to track running instances per workspace. Each instance reports `HasActiveChannels`. The `im` tool's `send` with `auto_start=true` checks `OtherInstancesHaveActiveChannels()` before starting a competing connection to avoid conflicts (e.g., Telegram bot duplicate polling).
  - **Workspace-scoped bindings**: `reloadBindingLocked()` loads bindings via `bindingStore.ListByWorkspace(m.session.Workspace)`. The `im` tool's `status` action only shows current-workspace adapters.
- **TUI** (`internal/tui/`): Bubble Tea program with multiple panels (model picker, provider picker, MCP panel, IM panel, inspector, harness panel, skills panel, preview panel). Supports i18n (`en` / `zh-CN`). Includes a fullscreen file browser with side-by-side preview, live markdown rendering, and status-bar-first loading feedback. Three input modes: normal (`❯`), shell (`$`/`!` prefix, one-shot), and chat (`#` prefix, persistent LAN Chat quick-send). **Extpane** (`internal/tui/extpane/`) opens real terminal tabs/windows for running sub-agents and teammates, streaming their output via `tail -f` logfiles. Three backends are auto-detected by priority: tmux (if `$TMUX` is set) > Kitty (`KITTY_WINDOW_ID`) > iTerm2 (`TERM_PROGRAM == "iTerm2"`). Each backend implements `CreateTab`/`CloseTab`/`SetTitle`. Safety: `maxPanes=10` hard cap, `failed[agentID]` permanent blocklist after first failure, self-window ID capture prevents killing ggcode's own tab/window.
- **WebUI** (`internal/webui/`): HTTP server with REST API for config/session management + WebSocket chat. Works in both TUI and daemon modes via `ChatBridge` interface. In daemon mode, `DaemonBridge` injects webchat messages through `pendingInterruptions` into the agent loop. In TUI mode, `TUIChatBridge` routes messages through `program.Send()` into the bubbletea event loop — identical to keyboard input. Agent streaming events are broadcast to all connected WebSocket clients. SPA (frontend) served from embedded `dist/` or fallback to index.html.
- **Sub-agents** (`internal/subagent/`): Manager with semaphore-based concurrency, configurable timeout (default 30 min), progress tracking. Runner executes tasks in isolated agent instances. Sub-agent system prompts include the working directory so agents know their project root without discovery. Manager exposes `CancelAll()` to cancel all running sub-agents at once.
- **Daemon mode** (`internal/daemon/` + `cmd/ggcode/daemon.go`): Headless agent with terminal follow display, background forking, keyboard shortcuts (v/q/s output mode, M/U mute, f follow toggle, r restart). Uses same tool label system as TUI.
- **Knight** (`internal/knight/`): Background autonomous agent with daily token budget, activity-driven code monitoring.
- **Swarm/Teammates** (`internal/swarm/`): Team-based multi-agent coordination. Teammates are spawned with their own agent loop and inbox. System prompts include the working directory via `SetWorkingDir()`. `CancelAll()` cancels all working teammates across all teams (used on interrupt). Task board supports assignee-based direct delivery.
- **A2A** (`internal/a2a/`): Agent-to-Agent protocol with multi-auth server (apiKey, OAuth2+PKCE, Device Flow, OIDC+JWKS, mTLS), auto-negotiating client, local registry with PID-based instance detection, MCP bridge for transparent cross-instance tool calls. Instance-level config override via `.ggcode/a2a.yaml`.
- **Tunnel/Broker** (`internal/tunnel/`): WebSocket tunnel broker for mobile relay. `Broker` manages connected clients, records tunnel events to session JSONL via `AppendTunnelEventToDisk()`, and replays canonical events on reconnect. Supports active session tracking (`AnnounceActiveSession`), multi-session switching (`SwitchSession`), and in-flight text recovery. `RelayClient` connects to the relay server with backpressure (30s write deadline). Protocol events include text streaming, snapshots, tool results, and session metadata.
- **Relay server** (`ggcode-relay/`): Standalone binary that acts as a WebSocket relay between desktop instances and mobile clients. Rooms are keyed by workspace. Events are persisted to SQLite with deduplication by eventID. Client→server messages (mobile user input) are always forwarded to the server even if deduped, ensuring agent delivery after relay restarts. Supports `active_session` binding and `snapshot_reset` control events. Peer writes use blocking sends with write deadline instead of channel drops to prevent silent data loss.
- **Auth stack** (`internal/auth/`): Full authentication subsystem — OAuth2 PKCE and Device Flow flows, OIDC Discovery with JWKS key rotation, JWT validation (HS256/RS256/ECDSA), opaque token introspection, token cache with per-`{provider}-{clientID}` isolation (`~/.ggcode/oauth-tokens/`). Provider presets for GitHub, Google, Auth0, Azure.
- **LAN Chat** (`internal/lanchat/`): Decentralized P2P messaging between ggcode instances on the same LAN. Uses mDNS discovery (`_ggcode._tcp`) with a pure-Go implementation. Direct HTTP transport (not through a relay). Community API key (`ggcode-lan-a2a-v1`) for zero-config trust. Features: direct messages, broadcast (`to='*'`), team messaging (`send_team`), @agent routing with approval flow, file attachments, **identity management** (`/nick name@role@team` composes `name_role` humanNick with separate role + team fields; defaults: role=`developer`, team=`dev-team`), presence exchange carries workspace/project/languages/role/team, conflict auto-resolution with numeric suffix, per-session persistence (no global nick), read receipts. TUI integration via `#` quick-send mode and `/chat` panel. Desktop GUI integration via Wails bindings.

## Configuration

Config file: `~/.ggcode/ggcode.yaml` or `--config <path>`. See `ggcode.example.yaml` for the full schema.

Resolution order: `./ggcode.yaml` → `./.ggcode/ggcode.yaml` → `~/.ggcode/ggcode.yaml`. The `--config` flag overrides auto-detection.

Key concepts:
- **`vendor`**: Provider vendor name (e.g., `zai`, `anthropic`, `openai`, `google`, `deepseek`, `openrouter`, `groq`, `mistral`, `moonshot`, `kimi`, `minimax`, `ark`, `together`, `perplexity`, `github-copilot`)
- **`endpoint`**: Named endpoint within a vendor (e.g., `cn-coding-openai`)
- **`model`**: Active model override
- **`default_mode`**: Permission mode for **new** sessions (`supervised`, `plan`, `auto`, `bypass`, `autopilot`). Default is `supervised`. In-session mode switches are saved to session metadata, not this config.
- **`vendors.<name>.endpoints.<name>.protocol`**: One of `openai`, `anthropic`, `gemini`, `copilot`
- **`mcp_servers`**: List of MCP servers to start (command + args + env) or connect (URL + headers)
- **`plugins`**: External command-based tools
- **`lsp_servers`**: Optional LSP server binary overrides, keyed by language ID (e.g. `go`, `rust`, `typescript`, `python`). Each entry has `binary` (path) and optional `args` list. When set, takes priority over auto-detected servers.
- **`tool_permissions`**: Per-tool rules: `allow`, `ask`, `deny`
- **`allowed_dirs`**: Directories the agent may access
- **`max_iterations`**: Agent loop limit per user turn (0 = unlimited)
- **`im.output_mode`**: IM tool result delivery granularity: `verbose` (default), `quiet`, `summary`
- **`hooks`**: Lifecycle hooks for 5 events (`on_agent_start`, `on_agent_stop`, `on_stream_start`, `on_stream_stop`, `on_tool_use`). Each event accepts a list of hooks with `type: command` or `type: http`. Payload via stdin. HTTP hooks support HMAC-SHA256 signature. See [`docs/guide/hooks.md`](docs/guide/hooks.md).
- **`a2a.auth`**: A2A server authentication — multiple schemes can be enabled simultaneously:
  - **`a2a.auth.api_key`**: Shared secret (simplest)
  - **`a2a.auth.api_keys`**: List of additional keys — any match authenticates. Supports `${ENV_VAR}` expansion per entry.
  - **`a2a.auth.oauth2`**: OAuth2 + PKCE or Device Flow (`provider`, `client_id`, `client_secret`, `issuer_url`, `flow`, `scopes`)
  - **`a2a.auth.oidc`**: OpenID Connect layer on OAuth2 (same fields + `openid` scope)
  - **`a2a.auth.mtls`**: Mutual TLS (`cert_file`, `key_file`, `ca_file`)
- **`a2a.lan_discovery`**: Enable mDNS broadcast for LAN peer discovery (default `true`). Powers LAN Chat and A2A peer discovery. Always uses built-in community key (`ggcode-lan-a2a-v1`) as fallback via `EffectiveAPIKey()`.
- **`a2a.host`**: Always `0.0.0.0` (LAN accessible). Loopback addresses (`127.0.0.1`, `::1`, `localhost`) are automatically overridden to `0.0.0.0` because mDNS discovery and LAN Chat require LAN reachability. Override with explicit non-loopback value if needed.
- **`a2a.api_key`**: Legacy API key field (still works, `a2a.auth.api_key` takes priority)
- **API keys**: Use `${ENV_VAR}` syntax for env var expansion (e.g., `${ANTHROPIC_API_KEY}`)

Instance-level A2A override: `.ggcode/a2a.yaml` in workspace root.

Legacy `provider`/`providers` config keys are rejected with an error at load time.

### A2A Authentication Examples

```yaml
# Simplest: shared API key
a2a:
  auth:
    api_key: "my-secret-key"

# GitHub zero-config
a2a:
  auth:
    oauth2:
      provider: "github"

# Custom IdP with Device Flow
a2a:
  auth:
    oauth2:
      issuer_url: "https://idp.example.com"
      client_id: "ggcode-agent"
      client_secret: "xxx"
      flow: "device"

# All auth methods
a2a:
  auth:
    api_key: "shared-key"
    oauth2:
      provider: "github"
      flow: "device"
    oidc:
      provider: "google"
      client_id: "xxx"
    mtls:
      cert_file: ".ggcode/certs/server.pem"
      key_file: ".ggcode/certs/server.key"
      ca_file: ".ggcode/certs/ca.pem"
```

See [`docs/a2a-auth.md`](docs/a2a-auth.md) for the full authentication guide.

### IM Slash Commands (Daemon Mode)

Available in any IM channel connected to a ggcode daemon:

| Command | Description |
|---------|-------------|
| `/listim` | List all IM adapters with status (online/muted/active) |
| `/muteim <name>` | Mute a specific adapter (cannot mute yourself — use `/muteself`) |
| `/muteall` | Mute all adapters except the one you're messaging from |
| `/muteself` | Mute THIS adapter — stops all replies (use `/restart` from another adapter to recover) |
| `/restart` | Restart daemon (unmutes all — mute is in-memory, not persisted) |
| `/help` | Show available commands |

## CLI Modes

- **Desktop GUI**: `ggcode-desktop-wails` — Wails-based desktop application with React frontend, visual chat, IM integration, tool approval dialogs, session sidebar, LSP language server status panel with one-click install (user/global/project scope)
- **Interactive TUI**: `ggcode` — launches the full Bubble Tea TUI
- **Daemon mode**: `ggcode daemon` — headless agent with IM gateway; `--follow` for terminal follow display
- **Pipe mode**: `ggcode -p "prompt"` — non-interactive, sends prompt and outputs response
- **Resume**: `ggcode --resume <id>` — resume a previous session; `--resume` alone or `--resume-picker` opens a picker
- **Bypass**: `ggcode --bypass` — start in bypass permission mode
- **Harness**: `ggcode harness <subcommand>` — manage harness-engineering workflows
- **MCP**: `ggcode mcp <subcommand>` — MCP server management
- **Completion**: `ggcode completion <shell>` — generate shell completions (bash/zsh/fish/powershell)

## Runtime Permission Modes

| Mode | Behavior |
|------|----------|
| `supervised` | Default. Respects per-tool rules, asks for unspecified tools |
| `plan` | Read-only: allows `read_file`, `multi_file_read`, `list_directory`, `search_files`, `glob`, LSP tools, read-only git/web tools; denies writes/commands. `lanchat` is always allowed (see `IsAlwaysAllowedTool`) |
| `auto` | Allows safe operations, denies dangerous ones automatically |
| `bypass` | Allows almost everything, warns on critical operations |
| `autopilot` | Bypass permissions + automatically continues when model asks for input; escalates external blockers to `ask_user` |

## Built-in Tools

Registered in `internal/tool/builtin.go` (core tools) + `cmd/ggcode/root.go` and `internal/tui/repl.go` (additional tools):

**File operations**: `read_file`, `multi_file_read`, `write_file`, `edit_file`, `multi_edit_file`, `multi_file_edit`, `list_directory`, `search_files`, `glob`
**Execution** (7): `run_command`, `start_command`, `read_command_output`, `wait_command`, `stop_command`, `write_command_input`, `list_commands`
**Git** (11): `git_status`, `git_diff`, `git_log`, `git_add`, `git_commit`, `git_blame`, `git_show`, `git_branch_list`, `git_remote`, `git_stash`, `git_stash_list`
**Web** (2): `web_fetch`, `web_search`
**Search**: `grep` (ripgrep-based, supports regex, glob, file type, context lines)
**LSP**: `lsp_definition`, `lsp_references`, `lsp_hover`, `lsp_symbols`, `lsp_workspace_symbols`, `lsp_diagnostics`, `lsp_rename`, `lsp_code_actions`, `lsp_implementation`, `lsp_prepare_call_hierarchy`, `lsp_incoming_calls`, `lsp_outgoing_calls`. Servers are auto-detected from PATH and workspace files; user-configurable via `lsp_servers` in config. Desktop app Settings > Integrations > Language Servers shows detection status and one-click install (scope: user > global > project).
**Productivity** (3, in `builtin.go`): `ask_user`, `todo_write`, `switch_mode` (+ `save_memory` registered separately in `cmd/ggcode/root.go`)
**Agent** (3, registered in `internal/tui/repl.go`): `spawn_agent`, `wait_agent`, `list_agents`
**Swarm** (10, registered in `cmd/ggcode/root.go`): `team_create`, `team_delete`, `teammate_spawn`, `teammate_shutdown`, `teammate_list`, `swarm_task_create`, `swarm_task_claim`, `swarm_task_complete`, `swarm_task_list`, `send_message`
**MCP** (3, registered in `cmd/ggcode/root.go`): `list_mcp_capabilities`, `get_mcp_prompt`, `read_mcp_resource`
**Cron** (3, registered in `cmd/ggcode/root.go`): `cron_create`, `cron_delete`, `cron_list`
**Skill** (1, registered in `cmd/ggcode/root.go`): `skill`
**LAN Chat** (5, in `cmd/ggcode/root.go`): `lanchat` — list participants (with role, team, workspace, languages), send messages (DM, `to='*'` broadcast, `send_team` team-targeted), read history, manage @agent approvals
**IM** (1, in `builtin.go`): `im` — status (list adapters), mute/unmute (drop/reconnect adapter), disable/enable, send (with `auto_start` for muted/disabled adapters). Always allowed in all permission modes. Manager injected post-registration via `im.NewToolManagerAdapter()`.
**Other**: `sleep`, `notebook_edit`, `enter_worktree`, `exit_worktree`

### Slash Commands

| Command | Description |
|---------|-------------|
| `/help` | Show help (categorized: Session, Model, Development, Integrations, System) |
| `/cost` | Session token usage + estimated cost or coding plan name |
| `/diff` | Git diff in chat (`--cached`, `--stat`, `<file>`) |
| `/hooks` | View configured hooks + validation status |
| `/mode` | Show or switch permission mode |
| `/compact` | Trigger context compaction |
| `/model` | Switch model |
| `/vendor` | Switch vendor/endpoint |
| `/mcp` | MCP server management |
| `/session` | Session list / switch / new |
| `/clear` | Clear current conversation |

Plus dynamically registered MCP-adapted tools and external plugin tools.

## Project Memory Files

Loaded at startup by `internal/memory/project.go`:
- `GGCODE.md` — primary project conventions (this file)
- `AGENTS.md` — agent-specific instructions (used by TeamClaw)
- `CLAUDE.md` — Claude-specific instructions
- `COPILOT.md` — Copilot-specific instructions

Scan order: `~/.ggcode/<file>` → walk up from working dir → recursively scan subdirectories (only under project root with `.git` or existing memory files).

## Coding Conventions

- **Import alias**: `internal/context` is imported as `ctxpkg` to avoid shadowing the standard `context` package
- **Circular dependency prevention**: `internal/cost/types.go` defines `TokenUsage` locally instead of importing from `internal/provider`. Session store stores cost as `[]byte` JSON (`CostJSON`) to avoid importing `internal/cost`
- **Platform-specific files**: Use Go build tags for OS-specific code (e.g., `clipboard_darwin.go`, `clipboard_linux.go`, `clipboard_windows.go`, `run_command_unix.go` with `//go:build unix`, `run_command_other.go` with `//go:build !unix`)
- **Integration tests**: Files like `internal/provider/integration_test.go` skip when API keys are not set (env vars: `ZAI_API_KEY`, `GGCODE_ZAI_API_KEY`). CI and `scripts/dev/verify-ci.sh` both run `go test -tags=!integration ./...`, and the shared verify script also clears those env vars locally to match CI behavior.
- **A2A integration tests**: Tagged with `//go:build integration`. Run with `go test -tags=integration -run TestFiveInstanceMesh ./internal/a2a/`. Five instances with different auth methods verified in 0.22s.
- **Error handling**: Follow standard Go error wrapping patterns (`fmt.Errorf("...: %w", err)`)
- **TUI i18n**: All user-facing strings go through `internal/tui/i18n.go` (`en` / `zh-CN` catalogs)
- **Provider interface**: All providers must implement `provider.Provider` (Name, Chat, ChatStream, CountTokens)
- **Tool interface**: All tools must implement `tool.Tool` (Name, Description, Parameters, Execute)
- **Plugin interface**: Plugins must implement `plugin.Plugin` (Name, Tools, Init)
- **Commit style**: Conventional commits — `fix:`, `feat:`, `chore:`, `docs:`, `ci:`
- **Config validation**: Legacy `provider`/`providers` keys are explicitly rejected; only `vendor`/`endpoint`/`vendors` schema is supported
- **Session persistence**: JSONL format in `~/.ggcode/sessions/<id>.jsonl` with checkpoint support after summarize compaction. Meta records persist session-scoped preferences: `permission_mode` and `sidebar_visible` (using `*bool` to distinguish unset from explicitly-false).
- **Storage**: Harness uses JSON files for events/snapshots; sessions use JSONL format
- **Token persistence**: OAuth2 tokens cached to `~/.ggcode/oauth-tokens/{provider}-{clientID}.json` with 0600 permissions; per-client isolation prevents overwrites between instances
- **Mute is in-memory**: IM adapter mute state is not persisted to the binding store — daemon restart recovers all adapters

## Release & Distribution

- **⚠️ ALWAYS read `docs/release-process.md` first** before preparing any release. It contains the exact checklist, file list, and command flow.

- **⚠️ Release checklist — EVERY item must be done before tagging:**
  1. Create `docs/releases/vX.Y.Z.md`
  2. Bump `npm/package.json` → `"version": "X.Y.Z"`
  3. Bump `python/pyproject.toml` → `version = "X.Y.Z"`
  4. Bump mobile: `cd mobile/flutter && bash scripts/version_sync.sh X.Y.Z` (updates 4 files: `.build-number`, `pubspec.yaml`, `build.gradle.kts`, `Info.plist`)
  5. Update `GGCODE.md` → latest documented release pointer
  6. Update `docs/releases/README.md` → current release notes pointer
  7. Run `make verify-ci` or equivalent
  8. Stage ALL changed files including mobile (4 files from step 4)
  9. Commit: `release: vX.Y.Z`
  10. Push `main`
  11. Create and push tag `vX.Y.Z`
  12. Monitor GitHub Actions until ALL workflows pass
- **GoReleaser** (`.goreleaser.yaml`): Builds for linux/darwin/windows on amd64/arm64 with `CGO_ENABLED=0`. Produces tar.gz, zip (Windows), and packages (deb, rpm, apk, ipk, archlinux). SBOMs included.
- **Version info**: Injected at build time via `-X` ldflags into `internal/version` (Version, Commit, Date)
- **npm** (`npm/`): Wrapper that installs the GitHub Release binary
- **Python** (`python/`): PyPI package `ggcode`
- **Release notes** (`docs/releases/`): Tag-specific Markdown files used directly as GitHub Release bodies

## Common Gotchas

- The large compiled binary (`ggcode`) in repo root is a build artifact — it's gitignored
- `ggcode.yaml` (actual config) is gitignored; only `ggcode.example.yaml` is tracked
- `.ggcode/` directory (runtime data) is gitignored
- Integration tests require real API keys; the shared local verify script clears provider env vars before `go test -tags=!integration ./...` so local checks behave like CI
- The `internal/tui/` package is the largest (~17.6k LOC, 47+ files) — changes here need careful TUI regression testing
- Provider protocol adapters must handle both streaming and non-streaming responses
- Harness events and snapshots are stored as JSON files under `.ggcode/harness/`
- The `copilot` protocol uses GitHub's OAuth flow (not API key) — handled by `internal/auth/`
- Agent tools (`spawn_agent`, `wait_agent`, `list_agents`) are defined in `internal/tool/` but registered in `internal/tui/repl.go`, not in `builtin.go`
- `save_memory` and `skill` tools are registered at startup (in `cmd/ggcode/root.go`), not in `RegisterBuiltinTools`
- `ask_user` and `todo_write` are in `builtin.go`; `save_memory` is separate (needs auto-memory reference)
- `a2a.api_key` (legacy top-level) still works but `a2a.auth.api_key` takes priority — `a2aAPIKey()` helper resolves both
- GitHub OAuth Apps are **confidential clients** — `client_secret` is required for token exchange even with PKCE; Device Flow does not need it
- Token cache files use `{provider}-{clientID[:12]}` as filename — different clientIDs for the same provider won't overwrite each other
- `/muteall` uses `MuteAllExcept(selfAdapter)` — sender's adapter is never muted
- `/muteself` emits the warning message before muting (500ms delay) so the user actually receives it
- WebUI starts in both TUI and daemon modes on `127.0.0.1:0` (random port). In TUI mode, the URL is displayed as a system message inside the chat area (not stderr — any terminal output after raw mode corrupts rendering)
- `ChatBridge` interface decouples webui from agent implementation: `DaemonBridge` (daemon mode) and `TUIChatBridge` (TUI mode) both implement it
- TUI mode webchat messages go through `program.Send(webchatUserMsg)` → TUI's normal submit flow (idle → `startAgent`, busy → `queuePendingSubmission`). This avoids any direct agent access from webui, preventing concurrency issues
- WebUI WebSocket uses per-connection write goroutines (buffered channel of 256) to prevent concurrent read/write on gorilla/websocket
- `DaemonBridge.SendUserMessage` claims the run slot under a single mutex lock (TOCTOU-safe). The existing `SubmitInboundMessage` has the same pattern but was not fixed as daemon IM messages are typically serialized by the adapter
- **Interrupt/exit cascading**: ctrl+c/esc (single) calls `cancelActiveRun()` which now also calls `subAgentMgr.CancelAll()` and `swarmMgr.CancelAll()`, so all running sub-agents and swarm teammates are cancelled on interrupt, not just the main agent. Double ctrl+c, ctrl+d, and other exit paths call `shutdownAll()` with the same cascading cancel.
- **Follow strip grace period**: Completed/failed/cancelled sub-agents remain visible in the TUI follow strip for 1 minute (`subAgentGracePeriod`) so users can review results, then are removed to prevent clutter. Swarm teammate slots are managed separately (lifecycle via team deletion).
- **Sub-agent/teammate completion does not interrupt busy main agent**: When a sub-agent or teammate finishes while the main agent is busy, the completion is shown as a system message and follow strip update only — no `agentHint` is queued or injected into the main agent's conversation. Only when the main agent is idle does completion trigger a new agent loop.
- **Extpane backend detection priority**: When terminal environments nest (e.g. iTerm2 inside tmux, or Kitty inside tmux), tmux wins because `$TMUX` is checked first. This ensures tabs are created in the innermost session. Each backend captures its own window ID at init to avoid self-closure.
- **Extpane tmux hook suppression**: User tmux configs with `set-hook -g after-new-window 'command-prompt ...'` would block tab creation with an interactive rename prompt. The tmux backend temporarily suppresses this hook (`set-hook -g -u after-new-window`) before `new-window`, then restores it. Using `-d` (detached) instead breaks content rendering.
- **Extpane Kitty uses tabs not windows**: The Kitty backend uses `kitten @ launch --type=tab` (not `--type=window`) to create tabs, consistent with iTerm2 and tmux backends.
- **swarm_task_create assignee**: The `assignee` parameter is strongly recommended — always set it when you know which teammate should do the task. When set, the task is pushed directly to the assignee's inbox for immediate execution. Only leave empty when no specific teammate can be determined.
- **send_message vs swarm_task_create**: `send_message` is for unstructured follow-ups, clarifications, or non-tracked communication. For assigning tracked tasks to teammates, prefer `swarm_task_create` (which auto-delivers to the assignee's inbox). Do NOT use `send_message` to follow up on an already-assigned task.
- **Tunnel event completeness**: `Session.TunnelEventsComplete` marks whether a session's tunnel events are fully recorded. Only complete event sets are used for replay; incomplete ones fall back to snapshot-based recovery. `PrepareCurrentSessionTunnelLedger()` clears stale incomplete events before starting a new recording session.
- **Relay event deduplication**: `room.upsertHistoryEvent()` deduplicates relay events by sessionID+eventID instead of appending, preventing history bloat when events are replayed after reconnect. Events with empty eventID (e.g., `snapshot_reset`) are not persisted to SQLite.
- **Relay client messages always forwarded**: `handleClientEncrypted()` in `relay.go` forwards client→server messages to the server even when `appendEvent()` reports dedup (isNew=false). This prevents mobile messages from being silently dropped after a relay restart when history is hydrated from SQLite. The dedup check only controls SQLite persistence and broadcast dedup, not server forwarding.
- **snapshot_reset does not consume eventID**: `Broker.enqueueControl()` handles `snapshot_reset` without incrementing the event ordinal, ensuring event IDs remain contiguous after replay.
- **Shell/chat modes independent of agent state**: Shell (`$`/`!`) and chat (`#`) modes can be entered and used while the agent is running. Shell commands execute immediately via `submitShellCommand` without entering the agent queue. The `shellOwnedLoading` flag tracks whether shell "owns" the loading spinner — when the agent was already busy, shell completion does not clear the agent's loading state. Only normal-mode text enters the pending submission queue when the agent is busy.
- **LAN Chat agent availability**: Each participant's `agent_busy` field (true/false) indicates whether their agent is currently processing. The `lanchat list` output includes this field so LLMs can prefer idle agents when delegating. `Hub.SetAgentBusy()` is called automatically by TUI/Desktop on agent start/end and propagated via presence exchange.
- **LAN Chat agent message deduplication**: Agent-directed DMs (`@agent`) skip `persistMessage()` in the Hub (session JSONL is canonical), skip the `onMessage` callback (avoids duplicate system message), and skip `lanchat:message` event emission in Desktop. The agent loop renders the message as a user message — no separate system message needed.
- **LAN Chat messaging scope**: Three actions map to three scopes: `send` (DM, requires `to=<node_id>`), `send_team` (team broadcast, requires `team=<name>`), `broadcast` (all participants, no `to`/`team`). Using `send` with `to='*'` is equivalent to `broadcast` — prefer `broadcast` for clarity.
- **Tool call reconciliation**: `ContextManager.ReconcileToolCalls()` runs on session restore and at the start of each `RunStreamWithContent()`. It adds cancelled `tool_result` entries for unpaired `tool_use` blocks and removes orphan `tool_result` blocks — preventing LLM API errors from providers that require all tool_use to have matching tool_result.
- **TUI session auto-selection**: When `ggcode tui` starts without `--resume`, it iterates all workspace sessions (newest-first) via `ListForWorkspace()`, loading the first unlocked one. If all are locked, a new session is created. This enables N instances to each grab a different session automatically.
- **MCP protocol version negotiation**: The MCP client sends `2025-11-25` (latest) during initialize and accepts all known versions (`2024-11-05`, `2025-03-26`, `2025-06-18`, `2025-11-25`). The server's negotiated version is stored in `Client.negotiatedVersion`. Unknown versions are rejected.
- **MCP OAuth DCR health check**: After Dynamic Client Registration, the client polls the authorize endpoint with PKCE params to wait for the client_id to propagate. All 4xx responses are retried (not just 200). The retry loop is infinite with a status display in the MCP panel.
- **Microcompact vs precompact**: Microcompact (context exceeds soft limit) is now silent — no user message. Only LLM-triggered precompact (explicit compaction request) shows a system message. A 2-minute cooldown after precompact prevents tight compaction loops. Auto-compact thresholds raised from 0.65/0.75 to 0.80/0.88 of context window.
- **Fuzzy line match for edit_file**: When exact `old_text` matching fails, `edit_file` falls back to fuzzy matching — stripping leading whitespace and comparing line content. This handles tab/space mismatches in the original file.
- **lanchat & im always allowed**: The `lanchat` and `im` tools are always allowed in every permission mode (including plan mode) via `IsAlwaysAllowedTool()`. They are checked before mode-specific rules in `ConfigPolicy.Check()`.
- **Port files are session-scoped**: `~/.ggcode/run/<sessionID>.json` — one file per running instance, keyed by session ID. `readAtPath` auto-cleans stale files (dead PID or legacy format without `session_id`). Cleanup covers all exit paths: `defer` (ctrl-c/ctrl-d/SIGTERM), `preExecCleanup` hook (before `syscall.Exec` restart/tmux-enter), and auto-detection in `ReadAll`.
- **Permission mode and sidebar visibility are session-scoped**: Both persist to the session JSONL meta record (`permission_mode`, `sidebar_visible`), never to global `default_mode` config or global `sidebar_visible` config. Only `config set default_mode=X` (explicit settings write) changes the global default. New sessions inherit the global default; resumed sessions restore their saved values. TUI Ctrl+R toggles sidebar visibility per-session. Daemon has `daemonModeSwitcher` for session-scoped permission mode persistence via the `switch_mode` tool.
- **Autopilot Goal-directed execution**: In autopilot mode, the agent defines a Goal via `ask_user` at the start of each session. The Goal defines what "done" looks like. All work must serve the Goal. The agent ends with `GOAL_COMPLETE` on its own line. Continuation heuristics anchor to the original task to prevent drift.
- **safego.Go replaces bare goroutines**: All goroutines should use `safego.Go()` or `safego.GoWithContext()` with panic recovery. Bare `go func(){...}()` without recovery can crash the entire process on panic. The v1.3.92 concurrency fix addressed several bare goroutine locations.
- **Microcompact is silent**: Microcompact (context exceeds 0.80 soft limit) runs silently — no user message. Only LLM-triggered precompact (explicit compaction request at 0.88 hard limit) shows a system message. A 2-minute cooldown after precompact prevents tight compaction loops.
- **edit_file fuzzy line matching**: When exact `old_text` matching fails, `edit_file` falls back to fuzzy matching — stripping leading whitespace and comparing line content. This handles tab/space mismatches in the original file without manual normalization.
- **MCP protocol version negotiation**: The MCP client sends `2025-11-25` (latest) during initialize and accepts all known versions (`2024-11-05`, `2025-03-26`, `2025-06-18`, `2025-11-25`). Unknown versions are rejected. The negotiated version is stored in `Client.negotiatedVersion`.
- **Cron `queue_if_busy`**: `cron_create` supports `queue_if_busy` (default false). When true, the prompt is queued and runs after the current task finishes instead of being skipped when the agent is busy. Only recurring jobs are persisted to `~/.ggcode/cron-jobs.json`; one-shot reminders are in-memory only.
