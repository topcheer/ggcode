# Tool Gap Analysis: claude-code vs ggcode

> Generated: 2025-01-XX  
> claude-code source: `/Users/zhanju/ggai/claude-code-source/src/tools/`  
> ggcode source: `internal/tool/`

---

## 1. Tool Inventory Overview

### claude-code (40 tool directories)

| Category | Tools |
|----------|-------|
| **File Ops** | FileReadTool, FileWriteTool, FileEditTool, NotebookEditTool |
| **Search** | GlobTool, GrepTool |
| **Execution** | BashTool, PowerShellTool, REPLTool |
| **Git** | (via Bash) |
| **Web** | WebFetchTool, WebSearchTool |
| **LSP** | LSPTool (10 operations in one tool) |
| **MCP** | MCPTool, ListMcpResourcesTool, ReadMcpResourceTool, McpAuthTool |
| **Agent/SubAgent** | AgentTool (with built-in agents, fork, resume) |
| **Task V2** | TaskCreateTool, TaskGetTool, TaskListTool, TaskUpdateTool, TaskStopTool, TaskOutputTool |
| **Team/Swarm** | TeamCreateTool, TeamDeleteTool, SendMessageTool |
| **Plan Mode** | EnterPlanModeTool, ExitPlanModeTool |
| **Worktree** | EnterWorktreeTool, ExitWorktreeTool |
| **Productivity** | TodoWriteTool, AskUserQuestionTool, ConfigTool, SkillTool |
| **Cron** | ScheduleCronTool (CronCreateTool, CronDeleteTool, CronListTool, CronUpdateTool) |
| **Memory** | (via AgentTool memory system) |
| **Special** | SleepTool, BriefTool, ToolSearchTool, RemoteTriggerTool, SyntheticOutputTool |

### ggcode (tool files in `internal/tool/`)

| Category | Tools |
|----------|-------|
| **File Ops** | read_file, write_file, edit_file |
| **Search** | search_files, glob |
| **Execution** | run_command, start_command, read_command_output, wait_command, stop_command, write_command_input, list_commands |
| **Git** | git_status, git_diff, git_log |
| **Web** | web_fetch, web_search |
| **LSP** | 10 separate LSP tools (lsp_go_to_definition, lsp_references, lsp_hover, lsp_document_symbols, lsp_workspace_symbols, lsp_implementation, lsp_prepare_call_hierarchy, lsp_incoming_calls, lsp_outgoing_calls, lsp_rename, lsp_code_actions) |
| **MCP** | list_mcp_capabilities, get_mcp_prompt, read_mcp_resource |
| **Agent/SubAgent** | spawn_agent, wait_agent, list_agents |
| **Productivity** | ask_user, todo_write, save_memory, skill |

---

## 2. Tools Missing in ggcode (claude-code has, ggcode doesn't)

### P0 — Must-Have (Core workflow impact)

| # | Tool | Description | Why P0 |
|---|------|-------------|--------|
| 1 | **TaskCreateTool** | Create structured tasks with ID, subject, description, metadata | Foundation for multi-step task tracking. claude-code V2 tasks have dependencies (blocks/blockedBy), status lifecycle, hooks integration |
| 2 | **TaskGetTool** | Retrieve a task by ID with full details | Required for task-driven workflows |
| 3 | **TaskListTool** | List all tasks with status | Required for task-driven workflows |
| 4 | **TaskUpdateTool** | Update task status/subject/description/blocks/metadata | Core lifecycle management: pending→in_progress→completed, dependency management |
| 5 | **TaskStopTool** | Stop a running background task | Cleanup/control for long-running agents |
| 6 | **TaskOutputTool** | Get output of a background task by ID | Needed to retrieve async agent results |
| 7 | **SendMessageTool** | Send messages to teammates/agents, broadcast, structured messages (shutdown_request, plan_approval) | Core for multi-agent coordination. Without this, ggcode agents cannot communicate |
| 8 | **TeamCreateTool** | Create multi-agent swarm team with team file, lead registration | Foundation for swarm/orchestration workflows |
| 9 | **TeamDeleteTool** | Cleanup team directories and worktrees | Cleanup companion to TeamCreate |
| 10 | **EnterPlanModeTool** | Switch to read-only plan mode with context preparation | Critical for "think before code" workflows |
| 11 | **ExitPlanModeTool** | Present plan for user approval, restore permission mode | Companion to EnterPlanMode — plan mode is useless without exit |

### P1 — Important (Significant capability gap)

| # | Tool | Description | Why P1 |
|---|------|-------------|--------|
| 12 | **AgentTool — Built-in Agents** | ExploreAgent, PlanAgent, VerificationAgent, ClaudeCodeGuideAgent, StatuslineSetupAgent, GeneralPurposeAgent | claude-code ships 6 pre-defined agent types with tailored system prompts. ggcode has no built-in agent definitions |
| 13 | **AgentTool — Fork SubAgent** | Fork self with full conversation context, shared prompt cache | Major performance feature — fork inherits context, avoids re-explaining. ggcode's spawn_agent is always "fresh" |
| 14 | **AgentTool — Resume SubAgent** | Resume a stopped/evicted agent from disk transcript | Continuation of agent work without starting over |
| 15 | **AgentTool — Agent Memory** | Per-agent persistent memory (user/project/local scopes), snapshot sync | Agents can accumulate knowledge across sessions |
| 16 | **ScheduleCronTool** | Create/update/delete/list cron-based scheduled tasks | Autonomous periodic execution. claude-code has full CRUD: CronCreate, CronDelete, CronList, CronUpdate |
| 17 | **ConfigTool** | Read/write runtime config settings (model, theme, permissions, etc.) | claude-code can self-configure; ggcode requires manual config file edits |
| 18 | **NotebookEditTool** | Edit Jupyter .ipynb cells (replace/add/delete) | Data science workflows |
| 19 | **EnterWorktreeTool / ExitWorktreeTool** | Create isolated git worktree for parallel work | claude-code can work in isolated worktrees. ggcode has harness worktrees but not as agent-accessible tools |

### P2 — Nice-to-Have

| # | Tool | Description | Why P2 |
|---|------|-------------|--------|
| 20 | **SleepTool** | Wait for a specified duration (seconds/milliseconds) | Simple utility — useful for polling/waiting patterns |
| 21 | **BriefTool** | Send a brief message to the user (Kairos feature) | Lightweight notification channel |
| 22 | **ToolSearchTool** | Search/discover available tools by description | Helpful for large tool pools; ggcode has fewer tools currently |
| 23 | **RemoteTriggerTool** | Trigger actions on external systems (via OAuth) | Claude-specific cloud integration |
| 24 | **SyntheticOutputTool** | Testing/debugging tool for synthetic responses | Internal/testing use only |
| 25 | **McpAuthTool** | OAuth 2.1 authentication for MCP servers | Advanced MCP auth flow |
| 26 | **PowerShellTool** | Windows PowerShell execution | Platform-specific (ggcode uses run_command) |
| 27 | **REPLTool** | Interactive REPL sessions | Niche use case |

---

## 3. Tools with Significant Implementation Differences

### 3.1 AgentTool (claude-code) vs spawn_agent (ggcode) — **P0**

| Aspect | claude-code AgentTool | ggcode spawn_agent |
|--------|----------------------|-------------------|
| **Agent types** | `subagent_type` selects from 6+ built-in agents + custom Markdown/JSON agent definitions + plugin agents | No agent types; always generic |
| **Background mode** | `run_in_background` parameter, completion notifications | Always background (fire-and-forget goroutine) |
| **Fork** | Fork inherits full conversation context, shares prompt cache | Not supported |
| **Resume** | Resume stopped agents from disk transcripts | Not supported |
| **Agent memory** | Per-agent persistent memory (user/project/local) with snapshot sync | Not supported |
| **Agent display** | Color-coded agent display, TUI panel integration | Basic text output |
| **Tool filtering** | Per-agent allowlist/denylist from agent definition | Optional tool list in spawn call |
| **Permission mode** | Per-agent permission mode (from definition) | Inherits parent mode |
| **Isolation** | `isolation: "worktree"` for isolated git worktree execution | Not supported |
| **Name/Description** | `name` and `description` for TUI display | `displayTask` only |
| **Max turns** | `maxTurns` to limit agent agentic turns | Not supported |
| **Effort** | `effort` level for agent | Not supported |
| **Custom agents** | Load from `.claude/agents/*.md`, user/project/managed/policy layers | Not supported |
| **Plugin agents** | Load from plugins | Not supported |
| **Model selection** | Per-agent model override | Uses parent provider |

**Key sub-features ggcode is missing (all P0/P1):**

- **Built-in agent definitions** (P1): ExploreAgent (codebase exploration), PlanAgent (planning), VerificationAgent (post-implementation verification), GeneralPurposeAgent. These are defined in `builtInAgents.ts` and loaded at startup.
- **Fork subagent** (P1): `forkSubagent.ts` — spawns a copy of the current agent with full context. Uses `FORK_BOILERPLATE_TAG` for context injection. Critical for cache efficiency.
- **Resume subagent** (P1): `resumeAgent.ts` — resumes from disk transcript + metadata. Auto-resumes stopped agents on SendMessage.
- **Agent memory** (P1): `agentMemory.ts` — persistent memory per agent with user/project/local scopes. Loads memory into system prompt.
- **Agent color manager** (P2): `agentColorManager.ts` — color assignment for agent display.
- **Agent memory snapshots** (P2): `agentMemorySnapshot.ts` — snapshot sync between project and local memory.

### 3.2 Plan Mode (claude-code) vs Permission Modes (ggcode) — **P0**

| Aspect | claude-code | ggcode |
|--------|-------------|--------|
| **Dedicated tools** | `EnterPlanModeTool` + `ExitPlanModeV2Tool` | No plan mode tools |
| **Mode switching** | Agent calls EnterPlanMode → switches to `plan` permission mode → agent explores read-only → calls ExitPlanMode → user approves plan → restores previous mode | ggcode has `plan` permission mode but no tool-triggered transitions |
| **Plan persistence** | Plan saved to disk file, editable by user before approval | No plan file |
| **Plan approval** | User approval dialog in TUI (requires `isUserInteraction`) | Not applicable |
| **Teammate plan flow** | Teammates in plan-required mode: submit plan to team-lead via mailbox, leader approves/rejects | Not supported |
| **Interview phase** | Optional "interview phase" in plan mode (V2) | Not supported |
| **Context preparation** | `prepareContextForPlanMode()` strips dangerous permissions, sets up classifier | Not supported |
| **Auto-mode restore** | Remembers pre-plan mode, restores on exit with circuit-breaker safety | Not supported |

**ggcode has the `plan` permission mode** (defined in `internal/permission/mode.go`) but lacks the tool interface for the agent to trigger mode transitions. The agent cannot programmatically enter/exit plan mode.

### 3.3 TodoWriteTool — **P1 (minor gap)**

| Aspect | claude-code | ggcode |
|--------|-------------|--------|
| **Storage** | V2 task system with file-based persistence (shared with TaskCreate etc.) | `.ggcode/todos.json` file |
| **Schema** | id, content, status (pending/in_progress/done) | Same schema — matches closely |
| **Status constraint** | Only one `in_progress` at a time | Same constraint |
| **Task hooks** | Integration with TaskCreated/TaskCompleted hooks | No hooks |
| **Verification nudge** | Auto-nudge to run verification agent after 3+ completed todos | Not supported |

ggcode's TodoWrite is a **good match** for claude-code V1. The gap is the V2 Task system (TaskCreate/Get/List/Update/Stop) which is a separate, more powerful system.

### 3.4 AskUserTool — **Match ✓**

Both implementations are functionally equivalent:
- Same schema: title, questions (id, title, prompt, kind, choices, allow_freeform, placeholder)
- Same kinds: single, multi, text
- Same normalization logic (auto-generate IDs, trim whitespace)
- Same handler pattern (injectable handler for TUI integration)

### 3.5 LSP Tools — **ggcode advantage ✓**

ggcode **exceeds** claude-code here:
- claude-code: Single `LSP` tool with operation parameter
- ggcode: 11 separate tools (lsp_go_to_definition, lsp_references, lsp_hover, lsp_document_symbols, lsp_workspace_symbols, lsp_implementation, lsp_prepare_call_hierarchy, lsp_incoming_calls, lsp_outgoing_calls, lsp_rename, lsp_code_actions)
- ggcode also has `lsp_diagnostics` which claude-code doesn't expose as a separate tool

### 3.6 Command Execution — **ggcode advantage ✓**

ggcode has **richer** async command management:
- claude-code: `BashTool` (single execution) + limited background via agent
- ggcode: `run_command` + full background job lifecycle: `start_command`, `read_command_output`, `wait_command`, `stop_command`, `write_command_input`, `list_commands`

### 3.7 Git Tools — **ggcode advantage ✓**

ggcode has dedicated git tools:
- claude-code: Git operations via Bash commands only
- ggcode: `git_status`, `git_diff`, `git_log` as first-class tools

### 3.8 SaveMemory — **ggcode only**

ggcode has `save_memory` as a dedicated tool. claude-code handles agent memory through the Agent memory system (per-agent scope), not as a standalone tool.

---

## 4. Architecture Differences

### 4.1 Tool Definition Pattern

| Aspect | claude-code | ggcode |
|--------|-------------|--------|
| **Language** | TypeScript classes with Zod schemas | Go struct implementing `Tool` interface |
| **Schema** | `z.strictObject()` with lazy loading | `json.RawMessage` hand-written JSON Schema |
| **Validation** | Zod parse + `validateInput()` method | `json.Unmarshal` + manual checks |
| **Prompt** | Separate `prompt.ts` per tool | Inline `Description()` string |
| **Registration** | Dynamic tool pool based on permission context | `RegisterBuiltinTools()` + external registration |
| **Result type** | Structured output with Zod output schema + `mapToolResultToToolResultBlockParam` | `Result{Content, IsError, Images}` |

### 4.2 Agent/SubAgent Architecture

| Aspect | claude-code | ggcode |
|--------|-------------|--------|
| **Manager** | `LocalAgentTask` with registry, abort controllers | `subagent.Manager` with semaphore-based concurrency |
| **Communication** | `writeToMailbox()` + inbox polling | None (fire-and-forget) |
| **Lifecycle** | Spawn → Running → Stopped/Completed, with resume from disk | Spawn → Running → Completed/Failed |
| **Tracking** | Agent ID registry + name registry + disk transcripts | In-memory tracking only |
| **Completion** | Notification as user-role message in parent conversation | Polling via `wait_agent` / `list_agents` |

### 4.3 Swarm/Team Architecture

| Aspect | claude-code | ggcode |
|--------|-------------|--------|
| **Team file** | JSON team file on disk with members, colors, panes | Not implemented |
| **Backends** | tmux, iTerm2, in-process (with auto-detection) | Not implemented |
| **Mailbox** | File-based mailbox per teammate, polled on turn start | Not implemented |
| **Color system** | `agentColorManager.ts` with named colors | Not implemented |
| **Layout** | `teammateLayoutManager.ts` for tmux pane layout | Not implemented |
| **Task sharing** | Shared task list per team (team_name = task_list_id) | Not implemented |

---

## 5. Priority Summary

### P0 — Must Implement (blocks core use cases)

1. **Task V2 Tools** (TaskCreate, TaskGet, TaskList, TaskUpdate, TaskStop, TaskOutput) — Foundation for multi-step workflows with dependencies, blocking, hooks
2. **SendMessageTool** — Agent-to-agent communication, mailbox system
3. **TeamCreateTool / TeamDeleteTool** — Swarm orchestration
4. **EnterPlanModeTool / ExitPlanModeTool** — Plan-first workflows

### P1 — Important (significant capability improvement)

5. **Built-in Agent Definitions** — ExploreAgent, PlanAgent, VerificationAgent, GeneralPurposeAgent
6. **Fork SubAgent** — Context inheritance, prompt cache sharing
7. **Resume SubAgent** — Continue stopped agents
8. **Agent Memory System** — Persistent per-agent memory
9. **ScheduleCronTool** — Autonomous periodic execution
10. **ConfigTool** — Runtime configuration management
11. **NotebookEditTool** — Jupyter notebook support
12. **EnterWorktreeTool / ExitWorktreeTool** — Isolated git worktree sessions

### P2 — Nice-to-Have

13. **SleepTool** — Simple delay utility
14. **BriefTool** — Lightweight notifications
15. **ToolSearchTool** — Tool discovery
16. **RemoteTriggerTool** — External trigger integration
17. **McpAuthTool** — MCP OAuth flows
18. **Agent Color System** — Visual agent identification
19. **Agent Memory Snapshots** — Cross-session memory sync

---

## 6. Implementation Recommendations

### Phase 1: Task + Team Infrastructure (P0)
- Implement Task V2 tools in `internal/tool/task_tools.go` with file-based persistence
- Implement Team tools in `internal/tool/team_tools.go` with team file management
- Implement SendMessage in `internal/tool/send_message.go` with mailbox system
- Add plan mode tools in `internal/tool/plan_mode.go`

### Phase 2: Agent Enhancement (P1)
- Add built-in agent definitions to `internal/subagent/`
- Implement agent memory system in `internal/subagent/memory.go`
- Add fork/resume support to spawn_agent
- Add ScheduleCronTool to `internal/tool/cron.go`

### Phase 3: Polish (P2)
- SleepTool, ConfigTool, NotebookEditTool
- Worktree tools
- Agent display improvements

---

## 7. Tool Name Mapping

| claude-code | ggcode | Status |
|-------------|--------|--------|
| FileReadTool | read_file | ✅ Match |
| FileWriteTool | write_file | ✅ Match |
| FileEditTool | edit_file | ✅ Match |
| GlobTool | glob | ✅ Match |
| GrepTool | search_files | ✅ Match (different name) |
| BashTool | run_command | ✅ Match (different name) |
| WebFetchTool | web_fetch | ✅ Match |
| WebSearchTool | web_search | ✅ Match |
| LSPTool | lsp_* (11 tools) | ✅ ggcode richer |
| MCPTool | (via MCP adapter) | ✅ Implicit |
| ListMcpResourcesTool | list_mcp_capabilities | ✅ Match |
| ReadMcpResourceTool | read_mcp_resource | ✅ Match |
| TodoWriteTool | todo_write | ✅ Match (V1 level) |
| AskUserQuestionTool | ask_user | ✅ Match |
| SkillTool | skill | ✅ Match |
| — | save_memory | ✅ ggcode only |
| — | git_status/diff/log | ✅ ggcode only |
| — | start_command + 5 bg tools | ✅ ggcode only |
| AgentTool | spawn_agent + wait_agent + list_agents | ⚠️ Partial match |
| TaskCreateTool | — | ❌ Missing |
| TaskGetTool | — | ❌ Missing |
| TaskListTool | — | ❌ Missing |
| TaskUpdateTool | — | ❌ Missing |
| TaskStopTool | — | ❌ Missing |
| TaskOutputTool | — | ❌ Missing |
| SendMessageTool | — | ❌ Missing |
| TeamCreateTool | — | ❌ Missing |
| TeamDeleteTool | — | ❌ Missing |
| EnterPlanModeTool | — | ❌ Missing |
| ExitPlanModeTool | — | ❌ Missing |
| ScheduleCronTool | — | ❌ Missing |
| ConfigTool | — | ❌ Missing |
| NotebookEditTool | — | ❌ Missing |
| EnterWorktreeTool | — | ❌ Missing |
| ExitWorktreeTool | — | ❌ Missing |
| SleepTool | — | ❌ Missing |
| BriefTool | — | ❌ Missing |
| ToolSearchTool | — | ❌ Missing |
| RemoteTriggerTool | — | ❌ Missing |
| McpAuthTool | — | ❌ Missing |
| SyntheticOutputTool | — | ❌ Missing |
| PowerShellTool | — | ❌ Irrelevant (Windows) |
| REPLTool | — | ❌ Missing |
