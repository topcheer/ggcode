# Plan Persistence

When you approve a plan in plan mode (via `exit_plan_mode`), ggcode writes the
approved plan to disk before implementation begins:

```
.ggcode/plans/plan-<timestamp>.md
```

The file contains a dated header followed by the full plan text. The tool result
mentions the save path so the agent (and you) can reference it later.

## Why

Plans that live only in the conversation are ephemeral: a session end, context
compaction, or `/clear` erases them. Persisting approved plans to files follows
the plan-as-artifact practice adopted across coding agents (Codex CLI
`.codex/PLAN.md`, Claude Code `.claude/plans/`) and addresses the "ephemeral
plan artifact" failure mode identified in large-scale empirical studies of
agent plan files.

## Behavior

- Plans are saved when the plan is **approved** (exit plan mode), not when it is
  drafted or rejected.
- Persistence failure is non-fatal: the plan is still returned to the
  conversation, and the failure is recorded in debug logs.
- Files accumulate under `.ggcode/plans/`; the directory is not tracked by git
  (`.ggcode/` is ignored). Delete old plans manually when they are no longer
  needed.
- To resume work from a saved plan in a later session, mention the file path
  (for example `.ggcode/plans/plan-20260922-153000.md`) and ask the agent to
  read it.
