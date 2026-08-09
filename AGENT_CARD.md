# ggcode Agent Card

**System**: ggcode - AI coding agent for the terminal
**Version**: v1.3.188
**Last Updated**: 2025-12-01
**Framework**: 2025 AI Agent Index Categories

---

## 1. Product Overview

| Field | Value |
|-------|-------|
| **Release Date** | 2023-06-15 (initial), ongoing active development |
| **Pricing** | Open source (Apache 2.0), self-hosted |
| **Description** | Terminal-based AI coding agent with multi-provider LLM support, MCP integration, IM adapters, and harness-engineering workflows. Enables autonomous code editing, testing, and refactoring with permission-aware execution. |
| **Category** | CLI/Developer Tools Agent |

---

## 2. Company & Accountability

| Field | Value |
|-------|-------|
| **Developer** | topcheer/ggcode community |
| **Repository** | https://github.com/topcheer/ggcode |
| **Governance Documents** | GGCODE.md, AGENTS.md, CLAUDE.md, COPILOT.md (project memory system) |
| **Contact Mechanisms** | GitHub Issues, Discussions |
| **Safety Framework** | - Built-in permission modes (supervised, plan, auto, bypass, autopilot)
- Dangerous command detection (`internal/permission/dangerous.go`)
- File operation guards (`internal/agent/action_annihilate.go`, `arg_size_guard.go`)
- Read-only plan mode for exploration
- Comprehensive detector suite (191+ integrity checks) |

---

## 3. Technical Capabilities

### 3.1 Models

| Field | Value |
|-------|-------|
| **Backend Models** | Multi-provider support (OpenAI, Anthropic, Google, Kimi, Aliyun, Ark, MiniMax, MiMo, GitHub Copilot, ZAI) |
| **Model-Agnostic Design** | ✅ Yes - user selects vendor/endpoint/model via config |
| **Default Model** | Configurable via `vendor`, `endpoint`, `model` schema |

### 3.2 Tools

| Field | Value |
|-------|-------|
| **Built-in Tools** | 52+ tools across categories:
- File operations: `read_file`, `write_file`, `edit_file`, `multi_file_edit`, `multi_file_write`
- Search: `grep`, `search_files`, `code_search`, `glob`
- Git: `git_add`, `git_commit`, `git_diff`, `git_log`, `git_checkout`, `git_branch_list`, etc.
- Command execution: `run_command`, `start_command`, `wait_command`, `stop_command`
- Browser automation: `browser` (CDP-based, no Node.js dependency)
- Session management: `task_create`, `task_list`, `task_update`
- Memory: `save_memory`, `delete_memory`, `knowledge_graph`
- MCP: `list_mcp_capabilities`, `get_mcp_prompt`, `read_mcp_resource`
- And 30+ more (see `internal/tool/builtin.go`) |
| **Tool Registry** | Dynamic runtime registration with `Registry` type (thread-safe) |
| **Custom Tools** | ✅ Supported via MCP servers and plugins |

### 3.3 Memory

| Field | Value |
|-------|-------|
| **Working Memory** | Session-based JSONL store with compaction (`internal/session/`) |
| **Context Management** | Token counting, compaction, and budget management (`internal/context/`) |
| **Project Memory** | File-driven (GGCODE.md, AGENTS.md, CLAUDE.md, COPILOT.md) auto-loaded from `~/.ggcode` and project directories |
| **Persistent Memory** | `save_memory` for project-global patterns, `knowledge_graph` for structured facts |
| **Memory Injection** | Auto-injects project memory into agent prompts (`ApplyProjectMemoryToAgent`) |

### 3.4 Architecture

| Field | Value |
|-------|-------|
| **Core Loop** | `RunStreamWithContent` in `internal/agent/agent.go` (~250 lines) |
| **Concurrency Model** | Goroutine-safe with `sync.RWMutex`, `safego.Recover` for panic recovery |
| **State Management** | Bubble Tea TUI with `Model` struct (state management) |
| **Async Operations** | Background commands, streaming events, sub-agent spawning |

---

## 4. Autonomy & Control

| Field | Value |
|-------|-------|
| **Autonomy Level** | L2-L3 (user and agent collaboratively plan, delegate, and execute) |
| **Permission Modes** |
| - `supervised` | Default; asks confirmation for tool calls |
| - `plan` | Read-only exploration only |
| - `auto` | Safe operations auto-allowed |
| - `bypass` | Almost everything allowed |
| - `autopilot` | Bypass + autonomous goal-directed execution |
| **Approval Requirements** | User confirmation for sensitive operations (file edits, git commits, command execution) via `ApprovalFunc` callback |
| **Monitoring** | - Detailed action traces with chain-of-thought reasoning
- `debug.Log` ring buffer (category-based diagnostic logging)
- Session replay via JSONL history |
| **Execution Traces** | ✅ Full trace available in session history |
| **Emergency Stop** | ✅ User can interrupt via Ctrl+C or `/cancel` command |
| **Stop Mechanisms** | - Graceful shutdown with `shutdownCtx/shutdownCancel`
- `stop_command` for background jobs
- `/reset` to clear pending actions |

---

## 5. Ecosystem Interaction

| Field | Value |
|-------|-------|
| **MCP Support** | ✅ Full Model Context Protocol client (`internal/mcp/`) - connects to MCP servers and exposes tools |
| **A2A Support** | ✅ Full Agent-to-Agent protocol (`internal/a2a/`) - JSON-RPC over HTTP + SSE streaming, mDNS discovery |
| **Identification Protocol** | `User-Agent` header for HTTP requests, no cryptographic signing |
| **Interoperability Standards** | MCP (primary), A2A (agent coordination) |
| **Web Conduct** | - Respects robots.txt for browser automation
- Standard HTTP(S) requests
- No anti-bot bypass by design |
| **AI Nature Disclosure** | Agent acts on behalf of user (explicit in design), no default watermarking of generated content |

---

## 6. Safety, Evaluation, and Impact

### 6.1 Guardrails

| Field | Value |
|-------|-------|
| **Built-in Guardrails** |
| - Permission system | 5 modes with dangerous command detection |
| - File operation safety | `arg_size_guard.go` (argument size limits), `file_ops` validation |
| - Action annihilation | `action_annihilate.go` detects cancelling tool calls |
| - Context overflow protection | Auto-compaction, session reset on repeated empty responses |
| - Tool argument validation | Schema-based validation with JSON parameters |
| - Interactive command detection | Warnings for interactive commands (vim, nano, etc.) |
| **Prompt Injection Defense** | ✅ Basic protection through permission system |
| **Sandboxing** | No OS-level sandbox; relies on user permission controls |
| **VM Isolation** | ❌ Not implemented (agent runs on host system) |

### 6.2 Evaluations

| Field | Value |
|-------|-------|
| **Internal Safety Results** | Published via code documentation and research notes (docs/research/) |
| **Capability Benchmarks** | Test suite with `go test` (unit + integration), harness workflow E2E tests |
| **Agent-Specific System Cards** | ✅ This document (AGENT_CARD.md) |
| **Third-Party Testing** | ✅ CI/CD pipeline (GitHub Actions), CodeQL security analysis |
| **Bug Bounty / Vulnerability Disclosure** | GitHub Issues (public vulnerability reporting) |

### 6.3 Incidents & Known Issues

| Field | Value |
|-------|-------|
| **Documented Security Incidents** | None (as of 2025-12-01) |
| **Known Security Concerns** | - Agent operates on host system without sandbox (by design, assumes user trust)
- Long-running commands may consume resources (mitigated by timeout and user interruption)
- TUI `Model` is a large state object (165 lines of fields) - maintainability concern, not security |

### 6.4 Compliance

| Field | Value |
|-------|-------|
| **Compliance Standards** | Not formally certified (open source, self-hosted) |
| **Data Privacy** | All data processed locally (no cloud telemetry by default) |
| **Audit Logging** | Optional via `debug.Log` and session history |

---

## 7. Agent Behavior & Policies

### 7.1 Planning

| Field | Value |
|-------|-------|
| **Planning Approach** | LLM-driven decomposition via reasoning blocks (when provider supports) |
| **Long-term Planning** | ✅ Supported via `task_create` and task dependencies |
| **Dynamic Task Decomposition** | ✅ Agent breaks down complex goals into subgoals autonomously |

### 7.2 Tool Selection

| Field | Value |
|-------|-------|
| **Tool Selection Method** | LLM chooses from available tools based on task requirements |
| **Tool Pruning** | `dynamic_tool_pruning` detector removes unused tools from context |
| **Tool Effectiveness Tracking** | `tool_effectiveness_tracker.go` monitors tool success rates |

### 7.3 Error Handling

| Field | Value |
|-------|-------|
| **Error Classification** | `FriendlyError()` and `UserFacingErrorLang()` with i18n |
| **Retry Logic** | Provider-level retry with exponential backoff (distinguishes transient vs permanent errors) |
| **Error Recovery** | Agent receives error details and attempts recovery (e.g., fix syntax errors, retry with different approach) |
| **User Notification** | Clear error messages in TUI with actionable suggestions |

### 7.4 Context Engineering

| Field | Value |
|-------|-------|
| **Context Budget** | Configurable via context manager, auto-compaction when approaching limits |
| **Token Counting** | Per-message and session-level tracking (`internal/context/`) |
| **Compaction Strategy** | Removes older messages while preserving critical context |
| **Context Pinning** | `context_pinning` detector prevents unintended context loss |

---

## 8. Evaluation Metrics

| Metric | Value | Notes |
|--------|-------|-------|
| **Lines of Code** | ~101k LOC (non-test) | 43 internal packages |
| **Test Coverage** | High | Several packages have 140%+ test/code ratio (harness, a2a, webui) |
| **Built-in Tools** | 52+ | File, search, git, command, browser, memory, MCP, etc. |
| **IM Adapters** | 17+ | QQ, Telegram, Discord, Slack, Feishu, WeChat, DingTalk, etc. |
| **Providers Supported** | 10+ | OpenAI, Anthropic, Google, Kimi, Aliyun, Ark, MiniMax, MiMo, Copilot, ZAI |
| **Detectors/Integrity Checks** | 191+ | In `internal/agent/*_detect.go`, `*_track.go`, `*_guard.go` |
| **Concurrency Safety** | ✅ | `sync.RWMutex` for state, `safego.Recover` for goroutines |

---

## 9. References & Resources

| Resource | URL |
|----------|-----|
| **Repository** | https://github.com/topcheer/ggcode |
| **Documentation** | docs/ directory (architecture, design, guides) |
| **Research Notes** | docs/research/ (gap analyses, trend studies) |
| **Release Process** | docs/release-process.md |
| **Architecture Review** | memory/arch-review-report.md |
| **Agent Index Reference** | https://aiagentindex.mit.edu (2025 AI Agent Index) |

---

## 10. Changelog

| Date | Change |
|------|--------|
| 2025-12-01 | Initial Agent Card created (sa-104 research task) |
| | Documented technical capabilities, autonomy levels, safety measures |
| | Aligned with 2025 AI Agent Index framework |

---

**Disclaimer**: This Agent Card is based on public information and code analysis as of 2025-12-01. For the most up-to-date information, refer to the GitHub repository and documentation.
