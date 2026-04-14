> AI coding agent for the terminal. Go codebase with Bubble Tea TUI, multi-provider LLM support, MCP integration, and harness-engineering workflows.

## Quick Reference

| Item | Value |
|------|-------|
| Module | `github.com/topcheer/ggcode` |
| Go version | 1.26.1 |
| CLI framework | Cobra (`spf13/cobra`) |
| TUI framework | Bubble Tea / Lip Gloss (`charmbracelet/bubbletea`, `charmbracelet/lipgloss`) |
| Database | SQLite (`modernc.org/sqlite`, pure Go) — harness subsystem only; sessions use JSONL files |
| License | MIT |
| Build output | `bin/ggcode` |
| Latest documented release | [`v1.1.32`](docs/releases/v1.1.32.md) |

## Build & Validation

```bash
make build          # go build -o bin/ggcode ./cmd/ggcode
make test           # go test ./...
make lint           # go vet ./...
make install        # go install github.com/topcheer/ggcode/cmd/ggcode
make clean          # rm -rf bin/
```

CI (`.github/workflows/ci.yml`):
- `CGO_ENABLED=0 go build -o /tmp/ggcode ./cmd/ggcode`
- `go vet ./...`
- `go test -tags=!integration ./...`
- `gofmt -l .` must produce no output (separate `lint` job)

Local CI-aligned verification lives in `scripts/dev/verify-ci.sh`; it mirrors the same build/vet/test
chain and also clears provider integration-test env vars before running tests.

Linter config (`.golangci.yml`): `gofmt`, `govet`, `errcheck`, `staticcheck`, `unused`. Test files excluded from `errcheck`/`govet`.

## Project Layout

```
cmd/ggcode/            CLI entrypoint, root command, pipe mode, resume, harness/mcp subcommands
cmd/ggcode-installer/  Standalone Go installer that downloads release binaries
internal/              215 Go source files (~41.7k LOC non-test)
  agent/               Core agent loop, tool execution, autopilot continuation (agent.go, ~33k bytes)
  provider/            LLM provider adapters: OpenAI, Anthropic, Gemini, Copilot + retry logic
  im/                  IM gateway runtime, QQ adapter, pairing state, channel bindings, and outbound routing
  tui/                 Bubble Tea TUI: views, panels, slash commands, i18n (en/zh-CN), fullscreen file browser + preview
  tool/                Built-in tools (file ops, search, commands, git, web, agents, productivity)
  harness/             Harness-engineering workflow engine (~6.2k LOC, 28 files — task management, worktrees, review, release)
  mcp/                 MCP client: JSON-RPC, process management, install, migration, presets
  config/              YAML config loading, env expansion, API key handling, Anthropic bootstrap
  memory/              Project memory loading (GGCODE.md, AGENTS.md, etc.) + auto-memory persistence
  subagent/            Sub-agent spawning, tracking, coordination (manager + runner)
  commands/            Slash command registry (bundled + loaded), usage formatting, skill templates
  context/             Conversation context window management and tokenization (imported as `ctxpkg`)
  session/             JSONL-backed session persistence (NOT SQLite — sessions stored as .jsonl files)
  checkpoint/          In-memory file checkpointing for undo/revert support
  permission/          Permission modes + per-tool policy enforcement + sandbox + dangerous tool classification
  plugin/              External tool plugins (command-based, MCP-based)
  hooks/               Pre/post hooks runner
  cost/                Token usage tracking (local TokenUsage type to avoid circular deps)
  auth/                Auth store + GitHub Copilot token management (OAuth flow)
  image/               Image processing, clipboard integration (platform-specific: darwin, linux, windows)
  install/             Self-update and install logic
  update/              Version checking and auto-update
  debug/               Debug logging utilities
  diff/                Diff formatting
  version/             Build-time version/commit/date (injected via ldflags)
  util/                Shell quoting, text truncation
docs/                  Architecture docs, design notes, release notes, site content
npm/                   npm wrapper package (installs GitHub Release binary)
python/                Python wrapper (PyPI: ggcode)
scripts/               Release scripts, site scripts
config/                MCP preset configuration (mcporter.json)
```

## Architecture

- **Agent loop** (`internal/agent/agent.go`): Central loop sends user messages to the LLM, executes tool calls, feeds results back. Supports streaming, multi-turn, autopilot continuation, mid-run interruptions, auto-compaction, and loop guards.
- **Provider adapters** (`internal/provider/`): Each LLM provider (OpenAI, Anthropic, Gemini, Copilot) has a protocol-specific adapter. `registry.go` maps protocol names to adapters via `NewProvider()`. Supported protocols: `openai`, `anthropic`, `gemini`, `copilot`. All implement the `Provider` interface (Name, Chat, ChatStream, CountTokens). Retry logic handles transient failures.
- **Permission modes** (`internal/permission/mode.go`): Five modes in a cycle: `supervised → plan → auto → bypass → autopilot`. Each mode defines default tool allow/deny rules. Autopilot auto-escalates blocked states to `ask_user`. Dangerous tools are classified in `dangerous.go`.
- **Harness** (`internal/harness/`): Multi-step engineering workflow engine with task queues, dependency tracking, git worktrees, context management, drift detection, inbox, promotion, review, release automation, and a monitor. Uses SQLite for event storage.
- **IM runtime** (`internal/im/`): Workspace-bound IM routing, QQ transport, pairing, persisted bindings, and mirrored outbound delivery for remote chat surfaces.
- **TUI** (`internal/tui/`): Bubble Tea program with multiple panels (model picker, provider picker, MCP panel, inspector, harness panel, skills panel, preview panel). Supports i18n (`en` / `zh-CN`). Includes a fullscreen file browser with side-by-side preview, live markdown rendering, and status-bar-first loading feedback.
- **Sub-agents** (`internal/subagent/`): Manager with semaphore-based concurrency, configurable timeout (default 30 min), progress tracking. Runner executes tasks in isolated agent instances.

## Configuration

Config file: `~/.ggcode/ggcode.yaml` or `--config <path>`. See `ggcode.example.yaml` for the full schema.

Resolution order: `./ggcode.yaml` → `./.ggcode/ggcode.yaml` → `~/.ggcode/ggcode.yaml`. The `--config` flag overrides auto-detection.

Key concepts:
- **`vendor`**: Provider vendor name (e.g., `zai`, `anthropic`, `openai`, `google`, `deepseek`, `openrouter`, `groq`, `mistral`, `moonshot`, `kimi`, `minimax`, `ark`, `together`, `perplexity`, `github-copilot`)
- **`endpoint`**: Named endpoint within a vendor (e.g., `cn-coding-openai`)
- **`model`**: Active model override
- **`default_mode`**: Permission mode at startup (`supervised`, `plan`, `auto`, `bypass`, `autopilot`)
- **`vendors.<name>.endpoints.<name>.protocol`**: One of `openai`, `anthropic`, `gemini`, `copilot`
- **`mcp_servers`**: List of MCP servers to start (command + args + env) or connect (URL + headers)
- **`plugins`**: External command-based tools
- **`tool_permissions`**: Per-tool rules: `allow`, `ask`, `deny`
- **`allowed_dirs`**: Directories the agent may access
- **`max_iterations`**: Agent loop limit per user turn (0 = unlimited)
- **API keys**: Use `${ENV_VAR}` syntax for env var expansion (e.g., `${ANTHROPIC_API_KEY}`)

Legacy `provider`/`providers` config keys are rejected with an error at load time.

## CLI Modes

- **Interactive TUI**: `ggcode` — launches the full Bubble Tea TUI
- **Pipe mode**: `ggcode -p "prompt"` — non-interactive, sends prompt and outputs response
- **Resume**: `ggcode --resume <id>` — resume a previous session; `--resume` alone opens a picker
- **Bypass**: `ggcode --bypass` — start in bypass permission mode
- **Harness**: `ggcode harness <subcommand>` — manage harness-engineering workflows
- **MCP**: `ggcode mcp <subcommand>` — MCP server management
- **Completion**: `ggcode completion <shell>` — generate shell completions (bash/zsh/fish/powershell)

## Runtime Permission Modes

| Mode | Behavior |
|------|----------|
| `supervised` | Default. Respects per-tool rules, asks for unspecified tools |
| `plan` | Read-only: allows `read_file`, `list_directory`, `search_files`; denies writes/commands |
| `auto` | Allows safe operations, denies dangerous ones automatically |
| `bypass` | Allows almost everything, warns on critical operations |
| `autopilot` | Bypass permissions + automatically continues when model asks for input; escalates external blockers to `ask_user` |

## Built-in Tools

Registered in `internal/tool/builtin.go` (core tools) + `cmd/ggcode/root.go` and `internal/tui/repl.go` (additional tools):

**File operations** (6): `read_file`, `write_file`, `edit_file`, `list_directory`, `search_files`, `glob`
**Execution** (7): `run_command`, `start_command`, `read_command_output`, `wait_command`, `stop_command`, `write_command_input`, `list_commands`
**Git** (3): `git_status`, `git_diff`, `git_log`
**Web** (2): `web_fetch`, `web_search`
**Productivity** (3, in `builtin.go`): `ask_user`, `todo_write` (+ `save_memory` registered separately in `cmd/ggcode/root.go`)
**Agent** (3, registered in `internal/tui/repl.go`): `spawn_agent`, `wait_agent`, `list_agents`
**MCP** (3, registered in `cmd/ggcode/root.go`): `list_mcp_capabilities`, `get_mcp_prompt`, `read_mcp_resource`
**Skill** (1, registered in `cmd/ggcode/root.go`): `skill`

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
- **Error handling**: Follow standard Go error wrapping patterns (`fmt.Errorf("...: %w", err)`)
- **TUI i18n**: All user-facing strings go through `internal/tui/i18n.go` (`en` / `zh-CN` catalogs)
- **Provider interface**: All providers must implement `provider.Provider` (Name, Chat, ChatStream, CountTokens)
- **Tool interface**: All tools must implement `tool.Tool` (Name, Description, Parameters, Execute)
- **Plugin interface**: Plugins must implement `plugin.Plugin` (Name, Tools, Init)
- **Commit style**: Conventional commits — `fix:`, `feat:`, `chore:`, `docs:`, `ci:`
- **Config validation**: Legacy `provider`/`providers` keys are explicitly rejected; only `vendor`/`endpoint`/`vendors` schema is supported
- **Session persistence**: JSONL format in `~/.ggcode/sessions/<id>.jsonl`, not SQLite
- **SQLite usage**: Only used by `internal/harness/` for event/snapshot storage

## Release & Distribution

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
- The `internal/tui/` package is the largest (~17.6k LOC, 41 files) — changes here need careful TUI regression testing
- Provider protocol adapters must handle both streaming and non-streaming responses
- `modernc.org/sqlite` is a pure-Go SQLite implementation (no CGO needed), used only by harness
- The `copilot` protocol uses GitHub's OAuth flow (not API key) — handled by `internal/auth/`
- Agent tools (`spawn_agent`, `wait_agent`, `list_agents`) are defined in `internal/tool/` but registered in `internal/tui/repl.go`, not in `builtin.go`
- `save_memory` and `skill` tools are registered at startup (in `cmd/ggcode/root.go`), not in `RegisterBuiltinTools`
- `ask_user` and `todo_write` are in `builtin.go`; `save_memory` is separate (needs auto-memory reference)
