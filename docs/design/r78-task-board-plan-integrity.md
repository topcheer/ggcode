# r78: Task Board Plan-Graph Integrity (blocker state rendering + board sync)

Research basis (2026-09 web research): Verification-Aware Planning for
Multi-Agent Systems (EACL 2026) identifies "subtle misalignments in task
interpretation" - not flawed reasoning - as a primary source of execution
failure; MiRA (arXiv 2603.19685) shows long-horizon agents lose track of
subgoal state as new information arrives unless milestone state stays
accurate and adaptive.

## Problem 1: misleading `blocked by` rendering (bug fix)

`task_list` rendered `(blocked by task-1, task-2)` regardless of blocker
status. Blocker completion does not auto-transition the blocked task
(`blockedBy` is informational), so after every blocker finished, the task
still LOOKED blocked. Agents waiting on the board would skip or wait
forever on an actually-ready task.

Fix (`internal/task/manager.go` `SplitBlockers` + `internal/tool/task_tools.go`):

- all blockers completed -> `(ready: blockers task-1, task-2 completed)`
- mixed -> `(blocked by task-2; task-1 already done)`
- dangling blocker IDs count as open (defensive)

## Problem 2: structured task board not covered by plan-staleness detection (detector consolidation, not a new detector)

Mid-run stale-plan detection (`internal/agent/todo_staleness.go`) only
tracked `todo_write`. The richer plan representation - the structured
task board (`task_create`/`task_update`) - had zero mid-run abandonment
detection; its state only re-enters context via compaction `Digest`.

Fix: the SAME detector now records successful `task_create`/`task_update`
calls (`recordBoardUpdate`, wired in `agent.go` next to the `todo_write`
hook). `maybeRemindStaleTodo` includes incomplete board tasks in its lazy
incompleteness check and emits a board-specific reminder. No new Agent
field, no new state machine, no new injection site - one detector, two
plan representations.

## Tests

- `internal/tool/task_tools_test.go` `TestTaskList_BlockerCompletionState`
- `internal/agent/todo_staleness_test.go` `TestTodoStaleness_BoardUpdate`,
  `TestStaleTaskBoardReminderText`
