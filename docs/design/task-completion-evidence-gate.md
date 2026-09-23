# Task Completion Evidence Gate (sa-183)

## Research basis

LongHorizon-Harness (arXiv:2608.01964) reformulates long-horizon agent
execution as a task-state management problem. Its core finding: harnesses
that keep task execution, task state, and completion assessment inside the
same growing context let **incorrect self-assessments propagate into later
decisions**. The proposed Manage-Execute-Audit (MEA) loop updates task state
*only with facts independently verified from the environment*.

## Gap it closes

ggcode already externalized task state (`internal/task` task board) and
already owned mature verification-evidence predicates
(`internal/agent/unverified_claim.go`: `hasVerificationCommands` /
`hasVerificationTools`, hardened against false positives in #1521, #437,
#2500). But the two never met: `task_update` flipped tasks to
`completed` with zero environment-evidence check — the exact MEA auditor
role was missing from the task state machine.

This is an **integration** of existing detectors into the task board, not a
new detector.

## Design

```
task_update(status=completed)  [flip from pending/in_progress]
        │
        ▼
EvidenceFn()  (agent.TaskVerificationEvidence, cmd/ggcode/root.go wiring)
        │
   ┌────┴────┐
   ▼         ▼
 evidence  no evidence
   │         │
   ▼         ▼
metadata   metadata "verification": "unverified"
"verified" + bounded advisory appended to the tool result
```

- `internal/agent/task_evidence.go` — thread-safe predicate over the live
  run's `RunStats` (atomic pointer, `Agent.liveRunStats`), falling back to
  `lastRunStats` between runs. Reuses `hasVerificationCommands` (a FAILED
  build/test command does not count, #1521) and `hasVerificationTools`.
- `internal/tool/task_tools.go` — `TaskUpdateTool.EvidenceFn func() bool`;
  stamps the flip with `metadata["verification"] = "verified"|"unverified"`
  and appends a one-shot advisory per task, capped at 3 per process
  (`taskGateMaxWarns`). `nil` disables the gate (back-compat).
- Wiring: `cmd/ggcode/root.go` passes `agent.TaskVerificationEvidence(ag)`
  to `tui.REPL.SetTaskManager`.

## Semantics

- Only the *flip* to completed is gated; re-completing an already-completed
  task is not.
- Between runs the most recent run's evidence applies (users may complete
  planning tasks outside an LLM run); a live unverified run shadows a
  verified older run.
- Swarm teammates share the main agent's evidence baseline through cloned
  registries.
- The gate is advisory-only: it never blocks a completion, it annotates
  state (externalized evidence, per MEA) and nudges once.

## Tests

- `internal/tool/task_tools_evidence_test.go` — gate behavior, flip-only
  scoping, per-task one-shot, per-process cap, nil-EvidenceFn inertness.
- `internal/agent/task_evidence_test.go` — predicate semantics including
  failed-command exclusion and live/last-run precedence.
