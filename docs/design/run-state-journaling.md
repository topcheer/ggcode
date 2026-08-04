# Run State Journaling & Crash Recovery Detection

## Problem

When the ggcode agent process crashes during a run (SIGKILL, panic, OOM kill,
power loss, terminal close), there is no mechanism to detect this on the next
session load. The user resumes a session and has no idea their previous work
was interrupted unexpectedly. Uncommitted file edits from the crashed run are
left on disk with no context about what happened.

## Competitor Analysis

| Product      | Crash Detection Approach                          |
|-------------|--------------------------------------------------|
| Claude Code | Detects unexpected exits, offers session restore |
| Aider       | Auto-commits after each edit (git checkpoints)    |
| Cursor      | Maintains undo history + edit journal            |
| Windsurf    | Session state persistence with resume             |
| ggcode      | **None** (before this implementation)             |

## Solution: Run State Journal

A lightweight JSON journal file per session that tracks the lifecycle of each
agent run:

1. **Run Start**: `MarkRunning()` writes a `{"state": "running"}` entry with
   the session ID, PID, start time, and user prompt.

2. **Run Completion**: `MarkCompleted()` updates the journal to
   `{"state": "completed"}` with success status, iteration count, and files
   edited.

3. **Crash Detection**: If the process dies before `MarkCompleted()` runs,
   the journal stays in `"running"` state. On the next session load,
   `CheckCrashedRun()` detects the stale entry, verifies the PID is dead
   (not a concurrent process), and returns a recovery info struct.

4. **User Notification**: `FormatCrashRecoveryMessage()` produces a
   human-readable message that is injected into the TUI chat list when
   resuming a crashed session, advising the user to check uncommitted changes.

## Implementation

### Files

- `internal/agent/run_journal.go` - Core journal logic (MarkRunning,
  MarkCompleted, CheckCrashedRun, FormatCrashRecoveryMessage, CleanupOldJournals)
- `internal/agent/run_journal_test.go` - 12 tests covering all paths
- `internal/agent/agent.go` - Wired into RunStreamWithContent entry/exit
- `internal/agentruntime/sessions.go` - CheckCrashRecovery and CleanupJournals
  wrappers for the runtime layer
- `internal/tui/commands_slash.go` - Crash recovery message displayed when
  switching to a previously-crashed session

### Design Decisions

1. **Single JSON file per session** (`~/.config/ggcode/journals/<sessionID>_run_journal.json`)
   - Minimal I/O overhead (one write at run start, one at completion)
   - No database or complex persistence layer needed

2. **PID-based liveness check**: Before declaring a crash, verifies the PID
   from the journal is actually dead (via `kill -0`). Prevents false positives
   from concurrent ggcode instances.

3. **One-shot detection**: After crash detection, the stale journal is cleaned
   up. This prevents repeated crash messages on every session load.

4. **Graceful degradation**: All journal operations are best-effort with error
   logging. If the journal dir is unwritable or the file is corrupted, the
   agent continues normally without crash detection.

5. **Automatic cleanup**: `CleanupOldJournals()` removes journals older than
   24 hours to prevent unbounded accumulation.

### Journal Entry Format

```json
{
  "session_id": "abc123",
  "state": "running",
  "start_time": "2025-01-15T10:30:00Z",
  "end_time": "",
  "pid": 12345,
  "user_prompt": "implement feature X",
  "iterations": 0,
  "files_edited": 0,
  "success": false
}
```

### Recovery Message Example

```
[Crash Recovery] Your previous session was interrupted unexpectedly (crashed
or killed) approximately 2h ago. The last task was: "implement feature X".
Review any uncommitted file changes with git status or git diff before
continuing, as some edits from the interrupted run may be incomplete.
```

## Testing

12 tests cover:
- Journal creation on run start
- State update on run completion
- Crash detection for stale "running" entries with dead PIDs
- No false positives for completed sessions
- No false positives for missing journals
- Empty session ID edge case
- Recovery message formatting
- Nil recovery info edge case
- Corrupted journal cleanup
- Old journal cleanup
- No-op when journal file doesn't exist
- No-op for empty session IDs
