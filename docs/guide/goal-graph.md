# Goal Graph

The goal graph tool (`goal_graph`) provides a persistent, project-scoped hierarchical goal structure for long-horizon work. It stores a `goal -> milestone -> task` tree in `.ggcode/goal-graph.json` so progress survives across sessions.

Different from the session-scoped task board and todo list: those die with the session; the goal graph is the durable layer a new session discovers via `action=resume`.

## Actions

| Action | Description |
|---|---|
| `add` | Add a node (`level`: goal/milestone/task, `parent` for nesting - strict one-rank hierarchy). |
| `update` | Partial update: `status` (pending/in_progress/done/dropped), `title`, `next_step`. Reports descendant rollup. |
| `tree` | Render the full hierarchy with status markers and progress percentages. |
| `resume` | List open leaf nodes with their `next_step` - the break-point continuation entry for a new session. |
| `stats` | Counts by level/status, per-goal rollup, and >7-day stall flags. |
| `delete` | Delete a node and its subtree (cascade). |

## Typical flow

1. Start of a multi-day project: `add level=goal`, decompose into `milestone`s and `task`s, give tasks a `next_step`.
2. As work progresses: `update status=done` on completed tasks (parents report rollup progress).
3. New session: `action=resume` to get open leaves and continue where the last session stopped.
