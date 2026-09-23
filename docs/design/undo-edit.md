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

## Post-Session Rollback (`ggcode undo`)

The in-memory Manager has two blind spots: it dies with the process, and it
evicts entries beyond `maxCheckpoints` (50). A session that exits — or a crash
mid-refactor — leaves no way to roll its file edits back without resuming it.
This is the gap external tools like AgentUndo (local-first provenance and
rollback, https://agent-undo.com) were built for in 2026.

ggcode now closes it natively:

- **`internal/checkpoint/persist.go`** — `checkpoint.Persist` mirrors every
  `Save` to `<project>/.ggcode/undo/ggundo-<sessionID>.jsonl` (best-effort:
  write failures are logged via `debug.Log`, never block the edit). Enabled
  from `cmd/ggcode/root.go` (TUI, session rebound on `/sessions` / `/clear`
  via `REPL.SetCheckpointPersist`) and `cmd/ggcode/pipe.go` (pipe runs).
  The store self-prunes to the 10 newest sessions on first write.
- **`ggcode undo`** (`cmd/ggcode/undo_cmd.go`) — post-mortem rollback CLI:
  shows a per-file preview (created/modified + line counts), asks for
  confirmation, then restores every touched file to its pre-session state
  (first checkpoint per file wins; session-created files are removed).
  Flags: `--list`, `--session <id>`, `--yes`, `--dry-run`.

Limitations (inherent to checkpoint-based tracking): only edits made through
ggcode's file tools are recorded; shell-command side effects (`rm`, `git
checkout`, `sed -i`, ...) are invisible to both the preview and the rollback.
Restored files get mode 0644 (the original mode is not recorded).
