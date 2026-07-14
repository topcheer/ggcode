# Multi-Agent Execution Modes

> Last updated: 2026-07-15

ggcode has multiple mechanisms for delegating work to independent agents. Each differs in isolation level, tool access, and workspace semantics.

## Quick Comparison

| | Subagent | Swarm Teammate | Harness Worker |
|---|---|---|---|
| **Isolation** | Shared process, independent agent | Shared process, independent agent | **Independent process** |
| **Entry point** | `spawn_agent` tool | `teammate_spawn` tool | `ggcode harness` CLI |
| **Agent creation** | `AgentFactory` → `agent.NewAgent()` | `AgentFactory` → `agent.NewAgent()` | `BinaryRunner` → `exec.Command("ggcode", "--bypass")` |
| **Tool Registry** | Snapshot at spawn time | Shared reference (live) | Fresh instance (all tools) |
| **Workspace** | Main agent's cwd | Main agent's cwd | Git worktree (isolated) |
| **WorkingDir** | ✅ Propagated via `RunnerConfig.WorkingDir` | ✅ `SetWorkingDir` in factory | ✅ `cmd.Dir = worktree path` |
| **Observability** | Follow panel, events stream | Status events (idle/working) | Harness monitor, log files |
| **Result delivery** | `wait_agent` / `list_agents` | `teammate_results` / inbox | Harness task status + delivery report |

## Subagent

**Code**: `internal/subagent/` (manager.go, runner.go)  
**Spawned by**: `spawn_agent` tool, `skill` tool  
**Wire-up**: `internal/tui/repl.go` → `SetSubAgentManager()`

### Tool Access

Tools are sourced from the main agent's `*tool.Registry` at spawn time:

```
spawn_agent.go BuildToolSet():
  allToolInfo = t.Tools.List()  // snapshot of registry at this moment
  for each tool:
    exclude: spawn_agent, wait_agent, list_agents  (prevent recursion)
    register everything else
```

| Tool Category | Available? | Notes |
|---|---|---|
| Built-in (read_file, run_command, etc.) | ✅ | Shared instance |
| Skill tool | ✅ | Shared instance |
| MCP tools | ⚠️ | Only those registered at spawn time. MCP servers that connect **after** spawn are not available. |
| Agent tools (spawn_agent, etc.) | ❌ | Excluded to prevent recursion |

### Workspace

Working directory is propagated from the main agent:

```
RunnerConfig.WorkingDir → agent.SetWorkingDir() after creation
```

The subagent inherits the main agent's cwd, so file operations work relative to the project root without exploration.

### Lifecycle

1. `spawn_agent` tool → `Manager.Spawn()` → `safego.Go(subagent.Run)`
2. `subagent.Run()` creates independent agent via `AgentFactory`
3. Sets `WorkingDir`, runs agentic loop via `RunStream`
4. Text events accumulated per-turn (not per-chunk), stored in 200-event ring buffer
5. `Manager.Complete()` stores full output (no truncation)
6. Parent retrieves result via `wait_agent` or `list_agents`

---

## Swarm Teammate

**Code**: `internal/swarm/` (manager.go, idle_runner.go, team.go)  
**Spawned by**: `teammate_spawn` tool  
**Wire-up**: `cmd/ggcode/root.go` → `swarm.NewManager(cfg, prov, factory, builder)`

### Tool Access

All teammates share a reference to the main agent's `*tool.Registry`:

```
root.go:
  swarmToolBuilder := func(_ []string) interface{} {
      return registry  // shared reference, not a snapshot
  }
```

| Tool Category | Available? | Notes |
|---|---|---|
| Built-in tools | ✅ | Shared instance |
| Skill tool | ✅ | Shared instance |
| MCP tools | ✅ | **Live** — MCP tools registered after teammate spawn are also available, because the registry reference is shared |
| Swarm tools | ❌ | Not applicable |

### Workspace

Working directory set in the agent factory:

```
root.go:
  swarmAgentFactory := func(prov, tools, systemPrompt, maxTurns) {
      a := agent.NewAgent(prov, reg, systemPrompt, maxTurns)
      a.SetWorkingDir(ag.WorkingDir())  // main agent's cwd
      return a
  }
```

### Lifecycle

1. `teammate_spawn` → `Manager.SpawnTeammate()` → creates agent + starts `runTeammateLoop` goroutine
2. Teammate enters idle loop, polls team task board periodically
3. On task/message received → `handleMessage()` → `executeTask()` → `agent.RunStream()`
4. Output collected in `strings.Builder`, stored as `LastResult`
5. Leader retrieves via `teammate_results` or `send_message` reply channel

### Key Differences from Subagent

- **Persistent**: Teammate runs an idle loop and handles multiple tasks over its lifetime (subagent handles one task then exits)
- **Shared registry**: Gets live MCP tool updates; subagent gets a frozen snapshot
- **No event recording**: Teammate only collects final output text, no per-turn event stream
- **No follow panel**: Teammate execution is not observable in real-time (only status events)

---

## Harness Worker

**Code**: `internal/harness/` (run.go, worker.go)  
**Triggered by**: `ggcode harness run`, auto-run, `RunQueuedTasks()`  
**Wire-up**: `cmd/ggcode/root.go` → harness subcommand setup

### Tool Access

Harness launches a **completely independent ggcode process**:

```
run.go BinaryRunner.Run():
  cmd := exec.CommandContext(ctx, "ggcode", "--bypass", "--config", cfg, "--prompt", prompt)
  cmd.Dir = req.WorkingDir  // worktree path
```

| Tool Category | Available? | Notes |
|---|---|---|
| Built-in tools | ✅ | Fresh instance registers all tools |
| Skill tool | ✅ | Fresh instance loads skills |
| MCP tools | ✅ | Fresh instance connects MCP servers independently |
| Harness tools | ❌ | Worker uses restricted tool set via `--allowedDir` |

### Workspace

Isolated via git worktree:

```
run.go ExecuteTask():
  workspace := PrepareWorkspace(ctx, project, cfg, task)
  workingDir := workspace.Path  // worktree directory
  req := RunRequest{
      WorkingDir:          workingDir,
      AllowedDirs:         []string{workingDir},
      ReadOnlyAllowedDirs: harnessWorkerReadOnlyDirs(project),
  }
```

- Workspace is a git worktree created from HEAD (or a checkpoint branch)
- File system access restricted to the worktree via `--allowedDir`
- Read-only access to project root for shared files

### Lifecycle

1. `ExecuteTask()` → `PrepareWorkspace()` (git worktree)
2. `BuildRunPrompt()` constructs task-specific prompt with context
3. `BinaryRunner.Run()` or `executeTaskViaWorker()` starts the process
4. Worker writes output to stdout, captured in real-time
5. After completion: delivery report, drift detection, review/approval, promotion
6. Worktree preserved for review or removed after promotion

### Key Differences from Subagent/Teammate

- **Process isolation**: No shared memory, no shared registry, completely independent
- **Git worktree**: Changes are isolated; can be reviewed and promoted to main branch
- **Full tool suite**: Independent MCP connections, fresh skill loading
- **Governance**: Review → approve → promote workflow with delivery reports
- **No parent agent**: Worker is a standalone ggcode instance, not a sub-agent

---

## Decision Guide

| When to use | Mechanism |
|---|---|
| Quick parallel task, shared context | **Subagent** |
| Long-lived team with multiple tasks, role-based | **Swarm Teammate** |
| Isolated change with review/promotion, git safety | **Harness Worker** |
| Need MCP tools that aren't connected yet | **Swarm Teammate** (live registry) or **Harness Worker** (fresh process) |
| Need git isolation / code review | **Harness Worker** only |
| Real-time question/coordination with other ggcode instances on LAN | **LAN Chat** (`lanchat` tool) |
| Fire-and-forget code editing in another workspace | **A2A Remote** (`a2a_remote` tool) |
| Delegate to external CLI agent (Claude, Codex, Copilot, etc.) | **Delegate** (`delegate` tool) |
