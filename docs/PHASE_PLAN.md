# ggcode — Phased Implementation Plan

> Module: `github.com/topcheer/ggcode`
> Last updated: 2026-04-03

## Phase Overview

| Phase | Name | Duration (est.) | Goal |
|-------|------|-----------------|------|
| 1 | Foundation | 1–2 weeks | CLI skeleton, config, single provider (Anthropic), agent loop, 3 built-in tools, basic REPL |
| 2 | Tool System | 1–2 weeks | Full built-in tool set, permission system, streaming TUI polish |
| 3 | Context & Session | 1 week | Context manager with summarization, session persistence, cost tracking |
| 4 | MCP & Extensibility | 1–2 weeks | MCP client, plugin system, multi-provider support |
| 5 | Polish & Release | 1 week | Shell completion, man pages, install script, documentation, stress testing |

---

## Phase 1: Foundation

**Goal:** A working REPL that can talk to one LLM provider and execute basic file tools.

### Tasks

| # | Task | Dependencies | Deliverable |
|---|------|-------------|-------------|
| 1.1 | Initialize module (`go mod init`), add Cobra, Bubble Tea, glamour, anthropic SDK | — | `go.mod`, `cmd/ggcode/main.go`, `cmd/ggcode/root.go` |
| 1.2 | Config system: YAML load, `${ENV_VAR}` expansion, default config | 1.1 | `internal/config/config.go`, `internal/config/env.go`, `ggcode.yaml` |
| 1.3 | Provider interface + Anthropic implementation | 1.1 | `internal/provider/provider.go`, `internal/provider/anthropic.go` |
| 1.4 | Tool interface + Registry + `read_file`, `write_file`, `list_directory` | 1.1 | `internal/tool/tool.go`, `internal/tool/{read_file,write_file,list_dir}.go` |
| 1.5 | Agent loop: send messages, handle tool calls, feed results, stop condition | 1.3, 1.4 | `internal/agent/agent.go`, `internal/agent/loop.go` |
| 1.6 | Basic REPL: text input, send to agent, display text response | 1.5 | `internal/tui/app.go`, `internal/tui/repl.go` |
| 1.7 | Streaming: `ChatStream` with channel, Bubble Tea integration | 1.3, 1.6 | Streaming in provider + TUI `tea.Program.Send()` pattern |
| 1.8 | System prompt: configurable, injected as first message | 1.2 | System prompt handling in agent |

### Acceptance Criteria

- [ ] `ggcode` starts a REPL prompt
- [ ] User can type a message and get a streaming response from Anthropic
- [ ] Agent can read, list, and write files when requested
- [ ] Tool calls are displayed in the REPL
- [ ] Config loaded from `~/.ggcode/ggcode.yaml` with env var expansion
- [ ] Ctrl+C interrupts generation, Ctrl+D exits

### Verification

```bash
ggcode
> list the files in the current directory
> read the contents of go.mod
> create a file called hello.txt with "Hello, ggcode!"
> exit
# verify hello.txt exists
cat hello.txt
```

---

## Phase 2: Tool System & Permissions

**Goal:** Complete tool coverage, safe execution with permission gates, polished TUI.

### Tasks

| # | Task | Dependencies | Deliverable |
|---|------|-------------|-------------|
| 2.1 | `edit_file` tool (find/replace) | 1.4 | `internal/tool/edit_file.go` |
| 2.2 | `search_files` tool (grep/ripgrep) | 1.4 | `internal/tool/search_files.go` |
| 2.3 | `glob` tool | 1.4 | `internal/tool/glob.go` |
| 2.4 | `run_command` tool with timeout | 1.4 | `internal/tool/run_command.go` |
| 2.5 | `git_status`, `git_diff`, `git_log` tools | 1.4 | `internal/tool/git.go` |
| 2.6 | PermissionPolicy interface + config-based implementation | 1.2 | `internal/permission/policy.go` |
| 2.7 | Dangerous command detection | 2.4, 2.6 | `internal/permission/dangerous.go` |
| 2.8 | Path sandbox (allowed dirs, symlink resolution) | 2.6 | `internal/permission/sandbox.go` |
| 2.9 | Approval flow in TUI: display request, y/n/a keys, reply channel | 1.6, 2.6 | TUI approval UI + agent async bridge |
| 2.10 | Markdown rendering with glamour (syntax highlighting) | 1.6 | `internal/tui/markdown.go` |
| 2.11 | Diff preview for file edits | 2.1 | `internal/tui/diff.go` |
| 2.12 | Tool spinner component | 1.6 | `internal/tui/spinner.go` |
| 2.13 | Slash commands: `/help`, `/model`, `/clear` | 1.6 | `internal/tui/repl.go` (extended) |
| 2.14 | Input history (↑/↓) | 1.6 | `internal/tui/repl.go` (extended) |

### Acceptance Criteria

- [ ] All 8+ built-in tools work correctly
- [ ] `write_file`, `edit_file`, `run_command` trigger approval prompt
- [ ] Dangerous commands (e.g. `rm -rf /`) are blocked even with `allow` policy
- [ ] Path sandbox prevents access outside allowed directories
- [ ] File edits show diff preview before/after
- [ ] Markdown responses render with syntax highlighting
- [ ] Tool execution shows spinner with tool name
- [ ] Slash commands work (`/help`, `/model`, `/clear`)
- [ ] Input history navigable with arrow keys

### Verification

```bash
ggcode
> find all Go files in the project
> edit go.mod: change the module name to "test"
# verify diff is shown and approval is requested
> run: ls -la
# verify approval is requested
> run: rm -rf /
# verify dangerous command is blocked
> /help
> /clear
```

---

## Phase 3: Context & Session

**Goal:** Long conversations work without context overflow; sessions persist across restarts.

### Tasks

| # | Task | Dependencies | Deliverable |
|---|------|-------------|-------------|
| 3.1 | Token counting: Anthropic API, OpenAI tiktoken, Gemini heuristic | 1.3 | `internal/context/token_counter.go` |
| 3.2 | ContextManager: add messages, track tokens, check limits | 3.1 | `internal/context/manager.go` |
| 3.3 | Auto-summarization: trigger at threshold, LLM-based summary | 3.2 | `internal/context/summarizer.go` |
| 3.4 | `/compact` command to force summarization | 3.3 | TUI command |
| 3.5 | Session model + JSONL storage | — | `internal/session/session.go` |
| 3.6 | SessionStore: save, load, list, atomic writes | 3.5 | `internal/session/store.go` |
| 3.7 | Session index browsing | 3.6 | `internal/session/index.go` |
| 3.8 | Session resume: `ggcode --resume <id>` or `/resume` | 3.6 | CLI flag + TUI command |
| 3.9 | Export session to markdown | 3.6 | `internal/session/store.go` (ExportMarkdown) |
| 3.10 | Cost tracker with built-in pricing table | 1.3 | `internal/cost/tracker.go`, `internal/cost/pricing.go` |
| 3.11 | Cost display in TUI status bar + `/cost` command | 3.10 | `internal/tui/app.go` (status bar) |
| 3.12 | Session cleanup: remove sessions older than N days | 3.6 | `internal/session/store.go` (CleanupOlderThan) |
| 3.13 | Config watcher: hot reload with debounce | 1.2 | `internal/config/watcher.go` |

### Acceptance Criteria

- [ ] Long conversations (50+ turns) work without context errors
- [ ] Summarization triggers automatically when approaching token limit
- [ ] `/compact` works on demand
- [ ] Sessions auto-save and can be resumed after restart
- [ ] `/sessions` lists past sessions
- [ ] Exported sessions are valid markdown
- [ ] Cost tracking shows accurate per-session and cumulative totals
- [ ] Config changes hot-reload without restart

### Verification

```bash
ggcode
> (have a 30-turn conversation about refactoring code)
# verify no context overflow errors
> /compact
# verify conversation continues smoothly
> /cost
# verify token counts and cost displayed
> exit
ggcode --resume <latest-session-id>
# verify conversation is restored
/sessions
# verify session appears in list
```

---

## Phase 4: MCP & Multi-Provider

**Goal:** Connect external tools via MCP; support OpenAI and Gemini providers.

### Tasks

| # | Task | Dependencies | Deliverable |
|---|------|-------------|-------------|
| 4.1 | MCP protocol types: JSON-RPC 2.0, tool definitions, responses | — | `pkg/mcp/types.go`, `pkg/mcp/schema.go` |
| 4.2 | MCP stdio transport: spawn process, framed read/write | 4.1 | `internal/mcp/transport.go` |
| 4.3 | MCP client: initialize, list tools, call tools | 4.2 | `internal/mcp/client.go` |
| 4.4 | MCP tool → ggcode Tool adapter | 4.3, 1.4 | `internal/mcp/convert.go` |
| 4.5 | Config support for MCP server definitions | 4.3 | `internal/config/config.go` (mcp_servers field) |
| 4.6 | OpenAI provider implementation | 1.3 | `internal/provider/openai.go` |
| 4.7 | Gemini provider implementation | 1.3 | `internal/provider/gemini.go` |
| 4.8 | Provider registry: switch between providers at runtime | 4.6, 4.7 | `internal/provider/provider.go` (registry) |
| 4.9 | `/model` command to switch provider/model mid-session | 4.8 | TUI command |
| 4.10 | Plugin system: Go plugin loader + API | — | `internal/plugin/loader.go`, `internal/plugin/api.go` |
| 4.11 | Config support for plugin directory | 4.10 | `internal/config/config.go` (plugins field) |
| 4.12 | Error recovery: MCP server crashes, provider failover | 4.3 | Retry logic, graceful degradation |

### Acceptance Criteria

- [ ] MCP servers defined in config connect and expose tools to the agent
- [ ] Agent can call MCP tools same as built-in tools
- [ ] OpenAI and Gemini providers work with streaming and tool calling
- [ ] `/model` switches provider mid-session
- [ ] Go plugins load from configured directory and register tools
- [ ] MCP server crashes are handled gracefully (tools marked unavailable)

### Verification

```bash
ggcode
> /model openai
# verify switch to GPT-4o
> list the files in /tmp
# verify tool works with OpenAI
> /model anthropic
# verify switch back
> # (with MCP server configured in config)
> use the filesystem MCP tool to list files
# verify MCP tool execution
```

---

## Phase 5: Polish & Release

**Goal:** Production-ready release with documentation and install experience.

### Tasks

| # | Task | Dependencies | Deliverable |
|---|------|-------------|-------------|
| 5.1 | Shell completion (bash, zsh, fish) via Cobra | 1.1 | Completion scripts |
| 5.2 | Man pages generation | 1.1 | `man/ggcode.1` |
| 5.3 | Install script: detect platform, download binary, setup config | — | `scripts/install.sh` |
| 5.4 | GitHub Actions CI: lint, test, build matrix (linux/darwin/windows) | — | `.github/workflows/ci.yml` |
| 5.5 | GitHub Actions release: goreleaser with checksums | — | `.goreleaser.yml`, `.github/workflows/release.yml` |
| 5.6 | README.md: features, install, config reference, screenshots | — | `README.md` |
| 5.7 | CONTRIBUTING.md: dev setup, code style, PR process | — | `CONTRIBUTING.md` |
| 5.8 | Stress testing: long sessions, rapid tool calls, large files | 1–4 | Test report |
| 5.9 | Performance profiling: memory usage, goroutine leaks | 1–4 | Profile results |
| 5.10 | Homebrew formula (optional) | 5.5 | Homebrew tap |

### Acceptance Criteria

- [ ] `go install github.com/topcheer/ggcode/cmd/ggcode@latest` works
- [ ] Shell completion works in bash/zsh/fish
- [ ] CI passes on all platforms
- [ ] Release artifacts include checksums
- [ ] README is comprehensive with working examples
- [ ] No goroutine leaks in profiling
- [ ] Install script works on macOS and Linux

---

## Dependency Graph

```
Phase 1 ──► Phase 2 ──► Phase 3 ──► Phase 4 ──► Phase 5
                         ▲                          ▲
                         │                          │
                    (parallel start)           (parallel start)
```

- Phase 2 depends on Phase 1 (needs agent loop + basic tools)
- Phase 3 depends on Phase 1 (needs agent + provider for token counting)
- Phase 4 depends on Phase 1 (needs tool registry + provider interface)
- Phases 3 and 4 can partially overlap (MCP can start before session persistence)
- Phase 5 depends on all previous phases

## Risk Register

| Risk | Impact | Mitigation |
|------|--------|------------|
| Go plugin system requires same compiler version | High | Document requirement; consider WASM alternative for v2 |
| Provider SDK breaking changes | Medium | Pin SDK versions; abstract behind provider interface |
| Bubble Tea async approval complexity | Medium | Use channel bridge pattern (documented in patterns.md) |
| MCP server compatibility issues | Medium | Graceful degradation; per-server error handling |
| Context summarization quality | Medium | Configurable threshold; manual `/compact` fallback |
| Terminal rendering issues across platforms | Low | Test in iTerm2, Alacritty, Windows Terminal |
