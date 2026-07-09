# Multi-Agent & Distributed Systems Research — Area 5

**Date:** 2026-07-10  
**Rotation:** 5 of 5 (Multi-Agent & Distributed Systems Research)  
**Analyst:** ggcode research agent

---

## Key Findings

1. **Google's Agent Executor (May 2026) introduces "durable execution" as the new baseline for agent runtimes** — automatic snapshotting, event logging, resume-after-outage, trajectory branching, and connection recovery. ggcode has precompact snapshots and session persistence, but lacks trajectory branching (testing different paths from a checkpoint) and automatic resume-after-outage for the agent loop itself.

2. **Cursor 3.0's `/multitask` and `/best-of-n` represent a paradigm shift in multi-agent UX** — natural-language decomposition into parallel subtasks, each in isolated git worktrees, with automatic comparison and best-result surfacing. ggcode has worktree support and `spawn_agent`, but no natural-language "do these N things in parallel" shortcut or automatic result comparison.

3. **Google's Agent Substrate (Kubernetes for agents) reveals a scaling gap** — designed for "hundreds of millions of registered agents" with sub-second tool calls. ggcode's swarm system supports teams of 5-10 teammates; no path to elastic auto-scaling of agent compute.

4. **The A2A protocol has emerged as the de facto inter-agent communication standard** (50+ enterprise partners, Linux Foundation governance, 21.9K GitHub stars). ggcode already implements A2A (server + client + registry), which is a significant competitive advantage — but lacks A2A Agent Card discovery via `/.well-known/agent-card.json` and SSE streaming for task updates.

5. **LangGraph's "time travel" (checkpoint-based replay and branching) is a unique innovation** — inspect any prior checkpoint, resume from that point, test alternate paths. ggcode has `internal/checkpoint/` for file undo/revert, but doesn't checkpoint the full agent state (conversation + tool results + reasoning) for replay.

---

## Detailed Analysis

### A. Multi-Agent Framework Landscape (2026)

#### Framework Tier Comparison

| Framework | Architecture | Strength | Weakness | ggcode Equivalent |
|-----------|-------------|----------|----------|-------------------|
| **LangGraph** | Graph-based state machine | Durable execution, checkpointing, time travel | Complex setup, Python-only | Harness (partial), session JSONL |
| **CrewAI** | Crews + Flows (dual-mode) | Role-based agents, event-driven flows | Rigid roles, memory-hungry at scale | Swarm teams + task board |
| **AutoGen** | Conversational multi-turn | Flexible dialogue-based collaboration | Steep learning curve, hard to debug | Sub-agents + swarm messaging |
| **OpenAI Agents SDK** | Python-first with Handoffs | Intuitive, tracing built-in | Not enterprise-ready | spawn_agent + wait_agent |
| **MetaGPT** | SOP-driven roles | End-to-end software dev simulation | Fixed roles, resource-intensive | Not applicable (different domain) |
| **Google Agent Executor** | Distributed runtime | Durable execution, K8s scaling, federation | Google Cloud ecosystem tie-in | Not implemented |

#### ggcode's Position

ggcode occupies a unique niche: **terminal-native multi-agent orchestration** with real terminal tabs for teammates (extpane), LAN-based peer discovery (lanchat), and cross-workspace delegation (A2A). No competitor offers this combination:

| Feature | ggcode | LangGraph | CrewAI | Cursor 3.0 | Conductor |
|---------|--------|-----------|--------|------------|-----------|
| Terminal-native | Yes | No | No | No | No |
| LAN peer discovery | Yes (mDNS) | No | No | No | No |
| Real terminal tabs for agents | Yes (extpane) | No | No | No | No |
| Persistent teams | Yes (swarm) | No | Yes (Crews) | No | No |
| Shared task board | Yes | No | No | No | No |
| Cross-instance A2A | Yes | No | No | No | No |
| Parallel worktree agents | Yes (spawn_agent) | No | No | Yes (/multitask) | Yes |
| IM-based remote control | Yes (15 platforms) | No | No | No | No |
| Mobile companion | Yes (tunnel/relay) | No | No | No | No |
| Result comparison | No | No | No | Yes (/best-of-n) | Yes (merge UI) |

### B. Agent Communication Protocols

#### Protocol Stack (2025-2026)

The industry has converged on a **layered protocol stack**:

```
Layer 4: ANP (decentralized marketplaces, DIDs)
Layer 3: A2A (inter-agent collaboration, Agent Cards)  ← ggcode implements
Layer 2: ACP (multi-framework interoperability, brokered)
Layer 1: MCP (tool integration, LLM ↔ tools)           ← ggcode implements
```

**Key insight**: MCP and A2A are complementary, not competing. MCP handles the vertical (LLM-to-tools), A2A handles the horizontal (agent-to-agent). ggcode implements both — a significant advantage.

#### ggcode's A2A Implementation vs Standard

| Feature | A2A Standard | ggcode Implementation | Gap |
|---------|-------------|----------------------|-----|
| Agent Card discovery | `/.well-known/agent-card.json` | mDNS + registry | No HTTP well-known URI |
| Task lifecycle | submitted→working→completed/failed | `a2a_send_task` with polling | No SSE streaming for tasks |
| Streaming updates | SSE or push notifications | Polling only | Missing SSE endpoint |
| Authentication | OAuth2, mTLS, API Key, OIDC | All 4 implemented | None — best-in-class |
| Agent Card format | JSON with skills/capabilities | Partial (name, URL, capabilities) | Missing skills array |
| gRPC binding | Protocol Buffers over gRPC | JSON-RPC over HTTP | Not implemented (sufficient for LAN) |

#### ggcode's MCP Implementation

Already comprehensive: MCP client with process management, OAuth 2.1 auth, DCR health check, tool/prompt/resource access, multi-server support, presets. This is mature and production-ready.

### C. Durable Execution & State Management

#### Google Agent Executor (May 2026)

Google's Agent Executor introduces five capabilities that represent the state of the art:

1. **Durable execution**: Event log + snapshotting for automatic resume after outages. Agent can survive process crashes and resume from the last checkpoint.
2. **Secure isolation**: Sandboxed execution for each agent component.
3. **Session consistency**: Single-writer architecture prevents concurrent state corruption.
4. **Connection recovery**: Clients reconnect and backfill responses from the last seen sequence.
5. **Trajectory branching**: Checkpoints let you branch at any point, testing different paths.

#### LangGraph's Checkpoint Model

LangGraph's checkpointing enables:
- **Time travel**: Inspect any prior checkpoint, resume from that point
- **Replay**: Replay execution from a checkpoint with different parameters
- **Branching**: Test alternate paths without losing the original context
- **Audit trail**: Full inspection of how the agent reached a decision

#### ggcode's State Management

| Capability | ggcode Status | Gap |
|-----------|--------------|-----|
| Session persistence | JSONL with checkpoint after compaction | Good |
| File checkpointing | `internal/checkpoint/` for undo/revert | File-level only, not full state |
| Agent loop resume | Session restore on restart | Works but no automatic crash recovery |
| Trajectory branching | Not implemented | Cannot test alternate paths from a checkpoint |
| Event log | Tunnel events + JSONL records | Not structured for replay |
| Crash recovery | Manual restart + session resume | No automatic resume-after-outage |

### D. Parallel Agent Execution Patterns

#### Cursor 3.0's Approach

**`/multitask`**: Decomposes a request into independent subtasks, launches multiple async subagents in parallel (not queued). Each agent works in its own isolated worktree.

**`/best-of-n`**: Runs the same task across multiple worktrees (optionally with different models), compares outcomes, and surfaces the best result.

**Agent Tabs**: Multiple chat windows displayed side-by-side. Monitor three agents simultaneously, intervene in any one.

#### Conductor.build's Approach

Mac app that orchestrates parallel Claude Code and Codex agents, each in an isolated git worktree. Visual dashboard showing all agents' progress. Review and merge changes from a unified UI.

#### ggcode's Parallel Capabilities

- `spawn_agent`: One-shot sub-agents running in the same workspace
- `swarm_task_create`: Team task board with assignee-based delivery
- `enter_worktree`: Isolated git worktrees for testing
- extpane: Real terminal tabs for teammate output

**Missing**: Natural-language "do these N things in parallel" decomposition, automatic worktree-per-agent isolation, result comparison/best-of-n.

### E. Fault Tolerance & Retry Patterns

#### Industry Patterns

**Circuit Breaker** (from DAPH framework): After N consecutive failures, stop retrying and return an error immediately. Prevents cascading failures in distributed agent systems.

**Exponential Backoff with Jitter**: Standard retry pattern with randomized delay to prevent thundering herd.

**Saga Pattern**: Long-running transactions broken into compensable steps. If a step fails, run compensating actions for all previous steps.

#### ggcode's Current Fault Tolerance

| Mechanism | Implementation | Quality |
|-----------|---------------|---------|
| Provider retry | 20 attempts, exponential backoff, 30s cap | Good, but no jitter |
| Agent loop error recovery | Error-streak detection, reactive compaction | Good |
| Sub-agent timeout | 30-minute default | Adequate |
| Swarm task failure | Task remains on board, can be re-assigned | Basic |
| Worktree rollback | `exit_worktree` with `discard_changes` | Good |
| Session recovery | JSONL restore on restart | Good |

**Missing**: No circuit breaker pattern for repeatedly failing tools or providers. No compensating-action (saga) pattern for multi-step agent workflows.

---

## Gap Analysis

| Gap | Priority | Effort | Competitor/Source |
|-----|----------|--------|-------------------|
| **A2A SSE streaming** | High | Medium | A2A standard |
| **Trajectory branching** | High | High | LangGraph, Agent Executor |
| **Natural-language multitask** | High | Medium | Cursor 3.0 |
| **Best-of-n result comparison** | Medium | Medium | Cursor 3.0 |
| **A2A well-known discovery** | Medium | Low | A2A standard |
| **Crash recovery (auto-resume)** | Medium | High | Agent Executor |
| **Circuit breaker for tools** | Medium | Low | DAPH framework |
| **Agent Card with skills** | Medium | Low | A2A standard |
| **Retry jitter** | Low | Low | Universal pattern |
| **Saga/compensating actions** | Low | High | Distributed systems |
| **Worktree-per-agent auto-isolation** | Low | Medium | Cursor/Conductor |

---

## Actionable Recommendations

### 1. **A2A SSE Streaming for Task Updates** (High Impact / Medium Effort)

**Problem**: ggcode's A2A client polls for task status. The A2A standard supports SSE for real-time streaming, which is essential for long-running tasks.

**Solution**: Add an SSE endpoint to the A2A server (`/a2a/tasks/{id}/stream`) that streams task state transitions. Update the A2A client to prefer SSE when the server supports it (detected via Agent Card capabilities).

**Next step**: Add `SubscribeTask` handler in `internal/a2a/server.go`, add SSE response writer, update client to consume SSE events.

### 2. **Natural-Language Multitask Decomposition** (High Impact / Medium Effort)

**Problem**: Users must manually spawn sub-agents or create swarm tasks. Cursor 3.0's `/multitask` automatically decomposes a request into parallel subtasks.

**Solution**: Add a `multitask` tool (or slash command) that:
1. Takes a natural-language goal
2. Uses a lightweight LLM call to identify independent subtasks
3. Spawns each subtask as a sub-agent in an isolated worktree
4. Monitors all agents, reports when each completes
5. Merges results sequentially

**Next step**: Create `internal/tool/multitask.go` that calls the provider with a decomposition prompt, then uses `spawn_agent` for each identified subtask.

### 3. **A2A Well-Known Agent Card Discovery** (Medium Impact / Low Effort)

**Problem**: ggcode's A2A discovery uses mDNS (LAN only). The A2A standard specifies `/.well-known/agent-card.json` for HTTP-based discovery, enabling cross-network agent discovery.

**Solution**: Add an HTTP endpoint at `/.well-known/agent-card.json` on the A2A server that returns the standard Agent Card JSON. Update the client to try well-known URI discovery as a fallback when mDNS is unavailable.

**Next step**: Add route in `internal/a2a/server.go` for `/.well-known/agent-card.json`, return `AgentCard` struct as JSON.

### 4. **Circuit Breaker for Repeatedly Failing Tools** (Medium Impact / Low Effort)

**Problem**: When a tool fails repeatedly (e.g., LSP server crashed, network unreachable), the agent keeps retrying. There's no circuit breaker to stop after N failures.

**Solution**: Add a `circuitBreaker` to the agent loop that tracks per-tool failure counts. After 5 consecutive failures of the same tool, inject a message: "Tool X has failed 5 consecutive times. Consider using an alternative approach or fixing the underlying issue."

This is analogous to the existing error-streak detection but tool-specific rather than global.

**Next step**: Add `toolFailureCounts map[string]int` to the agent loop, check before each tool execution.

### 5. **Best-of-N Result Comparison** (Medium Impact / Medium Effort)

**Problem**: When solving a complex task, there's no way to run multiple approaches and compare results. Cursor's `/best-of-n` runs the same task across multiple worktrees and surfaces the best.

**Solution**: Add a `best_of_n` tool that:
1. Creates N worktrees from HEAD
2. Spawns N sub-agents (optionally with different models or prompts)
3. Waits for all to complete
4. Presents results side-by-side for user selection
5. Merges the selected result and cleans up other worktrees

**Next step**: Create `internal/tool/best_of_n.go` using `enter_worktree` + `spawn_agent` + `wait_agent`.

### 6. **Retry Backoff Jitter** (Low Impact / Low Effort)

**Problem**: Concurrent agents all retry simultaneously on API rate limits (thundering herd).

**Solution**: Add jitter to the exponential backoff in `internal/provider/retry.go`:
```go
delay := min(cap, base * 2^attempt) * (0.5 + rand.Float64())
```

**Next step**: One-line change in `retry.go`.

### 7. **Agent Card with Skills Array** (Medium Impact / Low Effort)

**Problem**: ggcode's A2A Agent Card includes name, URL, and capabilities but not the full `skills` array defined by the A2A standard.

**Solution**: Populate the `skills` field in the Agent Card with the agent's registered tools and their descriptions. This enables other agents to discover what this agent can do without probing.

**Next step**: Update `AgentCard` construction in `internal/a2a/server.go` to include skills derived from registered tools.

---

## Sources

- Multi-agent framework comparison: https://langcopilot.com/posts/2025-11-01-best-multi-agent-ai-frameworks-2026
- A2A protocol analysis: https://zylos.ai/research/2026-02-15-agent-to-agent-communication-protocols/
- Google Agent Executor: https://cloud.google.com/blog/products/ai-machine-learning/agent-executor-googles-distributed-agent-runtime/
- LangGraph guide: https://nerova.ai/guides/what-is-langgraph-stateful-ai-agent-orchestration-2026
- Cursor 3.0 /multitask: https://cursor.com/changelog/04-24-26
- Cursor 3.0 /best-of-n: https://cursor.com/changelog/3-0
- Conductor.build: https://www.conductor.build/
- Conductor guide: https://codepick.dev/en/guides/conductor-build-intro/
- Semantic Kernel Agent Framework: https://learn.microsoft.com/en-us/semantic-kernel/frameworks/agent/
- DAPH fault-tolerant harness: https://medium.com/@gwrx2005/fault-tolerant-distributed-ai-agent-harness-architecture-implementation-and-evaluation-674b25e46cdb
- AI Agent Retry Patterns: https://fast.io/resources/ai-agent-retry-patterns/
