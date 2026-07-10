# Competitor Analysis — Area 1 (Rotation 2)

**Date:** 2026-07-10  
**Rotation:** 1 of 5 (Competitor Analysis — second cycle)  
**Analyst:** ggcode research agent

---

## Key Findings

1. **Claude Code v2.1.197-205 (late Jun–Jul 2026) shipped massive background-agent overhaul** — background agents now run in separate daemon processes with automatic crash recovery, draft PR creation on completion, worktree isolation, and a unified `claude agents` view. This matches ggcode's harness + swarm + extpane but with tighter git integration (auto commit/push/PR).

2. **Codex CLI's "Smart Approvals" (guardian subagent) is a novel safety pattern** — instead of blindly auto-approving in full-auto mode, a lightweight LLM reviews pending actions and routes them (approve/escalate/block). ggcode's `dangerous.go` static classification is less sophisticated; a dynamic guardian could improve auto mode safety.

3. **Codex-Spark (1,000+ tok/s on Cerebras W3) represents a paradigm shift in real-time coding** — the agent feels like a live collaborator. Combined with persistent WebSocket connections (50% faster TTFT, 80% lower round-trip overhead), this sets a new UX bar. ggcode's provider layer doesn't support WebSocket persistent connections.

4. **GitHub Copilot CLI GA'd with built-in custom agents (Explore, Task, Plan, Code-review)** — agents that automatically delegate to specialized sub-agents. ggcode has `spawn_agent` with subagent types but lacks automatic delegation heuristics.

5. **Claude Code's `/doctor` full setup checkup and `/context` token visualization are UX differentiators** — proactive diagnostics that help users self-troubleshoot. ggcode has `/cost` and context pill but lacks a diagnostic health check.

---

## Detailed Analysis

### A. Claude Code (v2.1.197–205, Jun 29–Jul 8)

#### Claude Sonnet 5 (v2.1.197)
- **New default model** with native 1M-token context window
- Promotional pricing: $2/$10 per Mtok through August 31
- No longer uses mid-conversation system role for harness reminders (v2.1.201) — directly relevant to ggcode's system message merge work

#### Background Agents Overhaul (v2.1.198+)
Major architecture changes:
- **Subagents run in background by default** — Claude keeps working while they run, notified on completion
- **Daemon-based** — background agents survive terminal close, auto-resume after crashes
- **Auto draft PR**: background agents commit, push, and open draft PRs when finishing code work in worktrees
- **Agent notifications**: `Notification` hook fires `agent_needs_input` / `agent_completed`
- **Worktree isolation**: each agent gets its own git worktree
- **Crash recovery**: workers killed by daemon restart auto-resume from where they left off

**vs ggcode**: ggcode has harness worktrees + swarm teammates + extpane tabs, but lacks:
- Auto PR creation from completed agent work
- Daemon-based crash recovery for agent processes
- Worktree-per-agent automatic isolation

#### Workflow System (v2.1.202)
- **Dynamic workflows** with configurable size (small/medium/large agent counts)
- **OpenTelemetry attributes**: `workflow.run_id` and `workflow.name` for reconstructing workflow activity from OTel data
- Workflow scripts support TypeScript with unicode handling

**vs ggcode**: ggcode has no equivalent workflow orchestration system. The harness is task-based, not workflow-based.

#### /doctor Health Check (v2.1.205)
Full setup checkup that diagnoses and fixes issues. Alias: `/checkup`.

**vs ggcode**: No equivalent. ggcode has `ggcode status` (instance listing) but no proactive health diagnostics.

### B. Codex CLI (2026 Major Updates)

#### New Models
| Model | Speed | Context | Best For |
|-------|-------|---------|----------|
| gpt-5-codex | Standard | 128K | Flagship — primary work |
| gpt-5-codex-mini | 4x more usage | 128K | Subagents, exploration |
| GPT-5.3-Codex-Spark | **1,000+ tok/s** | 128K | Real-time pair programming |

**Codex-Spark significance**: First OpenAI model on Cerebras W3 hardware (not NVIDIA). Text-only at launch, ChatGPT Pro only. Makes the agent feel like a live collaborator, not a batch process.

**vs ggcode**: ggcode's provider layer doesn't support WebSocket persistent connections or model-specific optimizations. Adding a WebSocket transport option could reduce latency for providers that support it.

#### Subagents GA (v0.115.0, March 16)
- Up to **6 concurrent subagents**
- Built-in roles: `explorer` (read-only), `worker` (read-write), `default` (general)
- **Custom agents** via TOML config (`~/.codex/agents/<name>.toml`) with model, reasoning_effort, developer_instructions
- **Smart Approvals**: guardian subagent reviews actions in full-auto mode → approve/escalate/block

**vs ggcode**: ggcode's `spawn_agent` supports subagent_type and model override, and swarm teammates have persistent loops. Missing: automatic delegation heuristics (auto-routing to explorer vs worker) and Smart Approvals guardian pattern.

#### Hooks System
| Hook | Version | Trigger | ggcode Equivalent |
|------|---------|---------|-------------------|
| SessionStart | v0.114.0 | Session begins | `on_agent_start` ✅ |
| Stop | v0.114.0 | Session ends | `on_agent_stop` ✅ |
| userpromptsubmit | v0.116.0 | Before prompt enters history | **Not implemented** |
| PostToolUse | v0.117.0 (alpha) | After any tool executes | `on_tool_use` ✅ |

**Gap**: `userpromptsubmit` hook (modify/block prompt before it enters context) — useful for enterprise audit and policy enforcement.

#### Integrated Terminal Reading
Codex can now **read the integrated terminal** for the current thread — check if dev server is running, see build errors without pasting.

**vs ggcode**: Not implemented. The `run_command` tool is one-shot; there's no persistent terminal state awareness.

#### codex cloud Command
Launch cloud tasks, choose environments, apply diffs — all from terminal.

**vs ggcode**: `a2a_remote` provides cross-workspace delegation but not cloud execution environments.

#### Mid-Thread Conversation Forks
`/fork` from any earlier message, not just the latest turn.

**vs ggcode**: ggcode has session-based `/branch` but not mid-conversation forking.

### C. Cursor 3.0

#### Agents Window
Unified sidebar showing all running agents (local, cloud, remote SSH) with:
- Multi-repo layout
- Seamless local↔cloud agent handoff
- `/multitask`: decompose request into parallel subtasks
- `/best-of-n`: run across multiple worktrees, compare results

**vs ggcode**: Desktop TeamBoard partially addresses this. Missing: local↔cloud handoff, automatic multitask decomposition.

### D. GitHub Copilot CLI (GA Feb 2026)

#### Built-in Custom Agents
| Agent | Function | ggcode Equivalent |
|-------|----------|-------------------|
| Explore | Fast codebase analysis (read-only) | `spawn_agent` with Explore type |
| Task | Run commands (tests, builds) | `run_command` tool |
| Plan | Implementation planning | Plan mode + enter_plan_mode |
| Code-review | High-signal change review | Not built-in (uses harness review) |

Copilot automatically delegates to these agents when appropriate and runs them in parallel.

#### Context Management
- Auto-compaction at 95% token limit
- `/compact` manual trigger
- `/context` visualization with breakdown
- `--resume` with TAB to cycle sessions

**vs ggcode**: ggcode has auto-compaction, `/compact`, and context pill. The `/context` breakdown by tool group is more detailed than ggcode's context pill.

#### Automation Flags
- `--silent`, `--share`, `--share-gist`, `--available-tools`, `--excluded-tools`
- `GITHUB_ASKPASS` for CI/CD auth

**vs ggcode**: ggcode has `-p` pipe mode and `--bypass`. Missing: `--share-gist`, `--available-tools`/`--excluded-tools` per-session tool allowlist.

### E. Devin (2026)

#### Key Capabilities
- **SWE-bench**: ~27.3% verified (down from claimed 13.86% → now independently verified higher)
- **ACI (Agent Computer Interface)**: full browser + terminal + editor access
- **Knowledge Management**: session playbooks, shared knowledge base
- **Parallel agents**: up to 3 concurrent sessions on Enterprise plan
- **Pricing**: $500/mo (Enterprise), $20/mo (Individual)

**vs ggcode**: Devin is cloud-native with full browser access. ggcode's `browser` tool provides similar capability but locally. Devin's session playbooks are analogous to ggcode's ratchet rules + playbook.

---

## Gap Analysis

| Gap | Priority | Competitor | ggcode Status |
|-----|----------|------------|---------------|
| **Auto draft PR from completed agents** | High | Claude Code | Not implemented |
| **Smart Approvals (guardian subagent)** | High | Codex CLI | Static dangerous.go classification |
| **`/doctor` health diagnostics** | Medium | Claude Code | No equivalent |
| **`/context` token breakdown by group** | Medium | Copilot CLI | Context pill (less detailed) |
| **userpromptsubmit hook** | Medium | Codex CLI | Not in hooks system |
| **Mid-conversation fork** | Medium | Codex CLI | Session-level branch only |
| **WebSocket persistent provider connections** | Medium | Codex Spark | HTTP streaming only |
| **Auto delegation heuristics** | Medium | Copilot CLI | Manual spawn_agent |
| **Per-session tool allowlist/denylist** | Low | Copilot CLI | Global tool_permissions only |
| **Terminal state awareness** | Low | Codex CLI | One-shot run_command only |
| **`--share-gist` session sharing** | Low | Copilot CLI | Tunnel share (mobile) only |

---

## Actionable Recommendations

### 1. **Auto Draft PR from Completed Sub-agents** (High Impact / Medium Effort)

**Pattern**: Claude Code's background agents auto-commit, push, and open draft PRs when finishing work in worktrees.

**Implementation**: After a `spawn_agent` or `swarm_task_create` completes in a worktree:
1. Check if the worktree has uncommitted changes
2. Auto-commit with a generated message
3. Push to a branch named `agent/{agentID}`
4. Create a draft PR via `gh pr create --draft`
5. Notify the user with the PR URL

**Next step**: Add a `post_completion_actions` config to subagent runner, defaulting to `{auto_commit: true, auto_pr: false}`.

### 2. **Smart Approvals Guardian Pattern** (High Impact / Medium Effort)

**Pattern**: Codex's guardian subagent reviews actions before execution in auto mode.

**Implementation**: Before executing a tool classified as "dangerous" in auto mode, spawn a lightweight LLM call that:
1. Receives the tool name, arguments, and recent context
2. Returns: `approve` / `escalate` / `block` with reasoning
3. If `escalate`, fall through to `ask_user`

This is more nuanced than the current binary dangerous/safe classification.

**Next step**: Add `guardian_check` in `executeToolWithPermission()` when mode is auto/bypass and tool is dangerous.

### 3. **`/doctor` Health Diagnostics Command** (Medium Impact / Low Effort)

**Pattern**: Claude Code's `/doctor` runs a full setup checkup.

**Implementation**: A slash command that checks:
- Config file exists and is valid YAML
- API key is set and not expired
- At least one vendor/endpoint is configured
- MCP servers are reachable (ping)
- LSP servers are detected for workspace languages
- Session store is writable
- Disk space adequate
- Go binary version (if applicable)

**Next step**: Create `internal/tui/doctor.go` with `runDoctorCheck()` returning a diagnostic report.

### 4. **`/context` Token Breakdown Command** (Medium Impact / Low Effort)

**Pattern**: Copilot CLI's `/context` shows detailed token usage by category.

**Implementation**: A slash command that shows:
- System prompt: N tokens
- Tool definitions: N tokens
- Conversation messages: N tokens
- Tool results: N tokens (breakdown by tool name)
- Reasoning blocks: N tokens
- **Total: N / M (X%)**

**Next step**: Add to `context.Manager` a `TokenBreakdown()` method that categorizes messages, expose via `/context` slash command.

### 5. **`userpromptsubmit` Hook** (Medium Impact / Low Effort)

**Pattern**: Codex CLI's hook fires before a prompt enters conversation history, can modify or block it.

**Implementation**: Add a new hook event `on_user_prompt` in `internal/hooks/`:
1. Fires after user submits text but before it enters `contextManager.Add()`
2. Hook receives the raw prompt on stdin
3. Exit 0 = allow, Exit 1 = block (show error to user), modified stdout = replace prompt

**Next step**: Add `OnUserPrompt` to hooks runner, call between text input and `contextManager.Add()`.

### 6. **WebSocket Persistent Provider Connection** (Medium Impact / High Effort)

**Pattern**: Codex Spark's persistent WebSocket reduces TTFT by 50% and round-trip overhead by 80%.

**Implementation**: Add an optional WebSocket transport to the OpenAI provider:
1. Maintain a persistent connection per provider instance
2. Send streaming requests over the same connection
3. Fall back to HTTP if WebSocket unavailable

**Next step**: Research which providers (OpenAI, Anthropic) support WebSocket streaming APIs. Add a `ws_transport.go` option.

---

## Sources

- Claude Code releases: https://github.com/anthropics/claude-code/releases
- Claude Code release notes: https://support.claude.com/en/articles/12138966-release-notes
- Claude Sonnet 5 announcement: https://www.anthropic.com/news/claude-sonnet-5
- Cursor 3.0 blog: https://cursor.com/blog/cursor-3
- Cursor 3.0 changelog: https://cursor.com/changelog/3-0
- Codex CLI 2026 guide: https://codex.danielvaughan.com/2026/03/27/codex-cli-in-2026-whats-new/
- Codex CLI changelog: https://developers.openai.com/codex/changelog
- GitHub Copilot CLI GA: https://github.blog/changelog/2026-02-25-github-copilot-cli-is-now-generally-available/
- GitHub Copilot CLI enhanced agents: https://github.blog/changelog/2026-01-14-github-copilot-cli-enhanced-agents-context-management-and-new-ways-to-install/
- Devin AI review 2026: https://aitoolranked.com/blog/devin-ai-review
- Aider 2026 guide: https://www.deployhq.com/guides/aider
- Aider review 2026: https://codegen.com/ai-tools/aider/
