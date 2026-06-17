# Harness Workflow

The harness provides isolated coding tasks with automated checks, bounded contexts, dependency tracking, and a structured review-to-release cycle.

## Overview

Each task runs in its own git worktree under `.ggcode/worktrees/`, completely isolated from your working directory. Task state, events, and snapshots are persisted as JSON files under `.ggcode/harness/`. Configuration lives in `.ggcode/harness.yaml`.

Tasks flow through a lifecycle:

```
queue → run → review → approve/reject → promote → release
```

## Core Commands

### Initialize

Create the harness scaffold (config, context definitions, check scripts):

```bash
ggcode harness init --goal "Build a payment service"
```

Options:
- `--force` — overwrite existing scaffold files
- `--element <path>` — hint project elements for context suggestion (repeatable)
- `--context-hint <text>` — guide context generation (repeatable)

### Check

Run structural validation and configured check commands:

```bash
ggcode harness check
```

### Queue a Task

Add a task to the backlog with optional dependency ordering:

```bash
ggcode harness queue "Refactor auth middleware to use JWT" \
  --depends-on <task-id> \
  --context payments
```

### Run

Execute a queued task (or the entire backlog) in an isolated worktree:

```bash
ggcode harness run                          # run next or specified task
ggcode harness run --all-queued             # drain the entire queue
ggcode harness run --retry-failed           # also retry failed tasks
ggcode harness run --resume-interrupted     # resume tasks left in running state
```

### Rerun

Retry a single failed task by ID:

```bash
ggcode harness rerun <task-id>
```

### List Tasks

Show all tasks and their execution state:

```bash
ggcode harness tasks
```

## Monitoring & Contexts

### Monitor

Display persisted activity (supports live watch mode):

```bash
ggcode harness monitor
ggcode harness monitor --watch --interval 5s
```

### Contexts

Summarize harness state grouped by bounded context:

```bash
ggcode harness contexts
```

### Inbox

Show owner-centric actionable work (tasks awaiting your attention):

```bash
ggcode harness inbox --owner alice
```

Inbox subcommands:

```bash
ggcode harness inbox promote --owner alice     # batch-promote all approved
ggcode harness inbox retry --owner alice       # batch-retry all failed
```

## Review

List tasks waiting for review:

```bash
ggcode harness review
```

Approve or reject a completed task:

```bash
ggcode harness review approve <task-id> --note "LGTM"
ggcode harness review reject <task-id> --note "Fix error handling"
```

Rejected tasks re-enter the retry flow automatically.

## Promote

Merge approved changes into the main branch:

```bash
ggcode harness promote                # list promotable tasks
ggcode harness promote apply <task-id>
ggcode harness promote apply --all-approved
```

## Release

Batch promoted tasks into a tagged release:

```bash
ggcode harness release                          # show release plan
ggcode harness release apply --environment prod --note "v1.2.0"
ggcode harness release apply --group-by context --batch-id REL-001
```

Release rollouts (progressive delivery):

```bash
ggcode harness release rollouts                 # list wave rollouts
ggcode harness release rollouts advance <id>    # advance next wave
ggcode harness release rollouts pause <id>      # pause rollout
ggcode harness release rollouts resume <id>     # resume paused rollout
ggcode harness release rollouts abort <id>      # abort rollout
```

## Maintenance

### Doctor

Inspect harness health, structure, and recent task state:

```bash
ggcode harness doctor
```

### Garbage Collection

Archive stale runs and prune old logs:

```bash
ggcode harness gc
```

## Task Lifecycle

1. **Queue** — task goal added to backlog with optional dependencies and context binding.
2. **Run** — executes in an isolated worktree (`.ggcode/worktrees/`).
3. **Review** — inspect diffs, check results, and drift detection output.
4. **Approve / Reject** — approved tasks become promotable; rejected tasks re-enter retry.
5. **Promote** — approved changes merge into the main branch in dependency order.
6. **Release** — promoted tasks batch into tagged releases with optional wave-based rollouts.

Your working directory is never modified at any stage — all work happens inside isolated worktrees.
