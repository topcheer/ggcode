# Agent-Accessible Undo (undo_edit)

## Problem

AI coding agents make mistakes — a file edit introduces a syntax error, a refactor
goes in the wrong direction, or context compaction causes the agent to lose track
of the original file content. Without an undo mechanism, the agent's only option
is "fix forward": apply another edit to correct the previous one. This:

1. **Compounds errors** — each fix-forward edit can introduce new issues
2. **Wastes tokens** — the agent re-reads, re-analyzes, and re-edits instead of reverting
3. **Loses original state** — after context compaction, the exact original content may
   no longer be in the conversation, making accurate fix-forward impossible

## Competitor Analysis

| Product | Approach |
|---------|----------|
| **Claude Code** | Automatic checkpoint per prompt; 100 snapshots; `/rewind` command |
| **Cursor** | Checkpoint UI for navigating to previous code states |
| **Cline** | Workspace snapshots restore to any prior state |
| **Aider** | Git-based undo via `/undo` command |
| **ggcode (before)** | Internal checkpoint.Manager with Undo/Revert/List — only accessible via user Esc+Esc |

## Solution

Expose the existing `checkpoint.Manager` to the LLM via a new `undo_edit` tool.

### Design

The tool is **intercepted at the agent dispatch layer** (`agent_tool.go`) rather
than executed through the normal tool path. This is because the checkpoint manager
lives on the `Agent` struct and is not available to standalone tool execution.

**Three actions:**

1. **`undo`** — reverts the most recent file edit, restoring the original content
2. **`list`** — shows recent checkpoints with IDs and file paths
3. **`revert`** — rolls back to a specific checkpoint by ID (also reverts all later edits)

### Architecture

```
LLM calls undo_edit(action=undo)
  └─ Agent.executeTool()
       └─ intercept: tc.Name == "undo_edit"
            └─ Agent.executeUndoEdit()
                 └─ checkpoint.Manager.Undo()
                      └─ AtomicWriteFile(oldContent)
```

The tool struct (`UndoEditTool`) in `internal/tool/undo_edit.go` provides only the
JSON schema and description. When `Execute()` is called directly (outside the agent),
it returns a helpful error explaining the tool requires the agent runtime.

### Integration Points

- **`internal/tool/undo_edit.go`** — tool definition, schema, formatting helpers
- **`internal/tool/builtin.go`** — tool registration
- **`internal/agent/agent_tool.go`** — interception in `executeTool()`, `executeUndoEdit()` method
- **`internal/tool/labels.go`** — TUI label for tool call display
- **`internal/checkpoint/checkpoint.go`** — underlying checkpoint storage (unchanged)

### Interaction with User Esc+Esc

The user-facing Esc+Esc rewind and the agent-facing `undo_edit` tool share the same
`checkpoint.Manager` instance. This means:
- If the agent undoes an edit, the user's Esc+Esc history is also updated
- If the user rewinds via Esc+Esc, the agent's undo stack reflects the new state
- No synchronization issues — both paths use the same mutex-protected data structure

## Rollback Fence (side-effect disclosure & re-execution warning)

Checkpoint restore rolls back **file state only**. Commands the agent ran
before the restore (`git push`, `gh pr create`, `npm publish`, messages sent)
keep their external effects — and after a restore the model frequently
re-issues a semantically equal command with fresh arguments (new IDs,
timestamps, flags), duplicating irreversible side effects. This is the
"semantic rollback" problem studied by ACRFence (arXiv:2603.20625).

ggcode implements a deterministic, no-LLM variant in `internal/rollback`:

- **Record**: successful `run_command` / `start_command` executions are kept in
  a bounded rolling window on the agent.
- **Fence**: every restore path (`/undo`, `/undo-run`, checkpoint panel revert,
  `undo_edit` undo/revert) moves the commands executed since the restored
  checkpoint's timestamp into a *fenced* set.
- **Disclosure**: the restore output (TUI and agent-injected correction
  feedback) lists the fenced commands with an explicit note that their
  external effects were **not** rolled back.
- **Re-execution warning**: when a command matching a fenced entry
  (whitespace-normalized comparison) runs again, a `[rollback-fence]` warning
  is appended to the tool result telling the model to verify the earlier
  effect (e.g. list existing PRs) before repeating.

Key files: `internal/rollback/fence.go`, `internal/agent/agent_rollback_fence.go`,
wiring in `agent_tool.go` (`executeTool`, `executeUndoEditInner`),
`correction_feedback.go`, and the TUI restore paths in
`commands_slash_admin.go` / `inspector_panel.go`.
