# Premature Termination Guard

Research basis: arXiv 2606.29718 "Diagnosing and Mitigating Context Rot in
Long-horizon Search" (GAIR-NLP/SJTU 2026; four flagship models across three
benchmarks, five repeats each).

## The failure mode

Under extensive context, models give up or submit uncertain incorrect answers
long before the context window is exhausted ("premature termination"). The
rate is positively correlated with context length even when task difficulty is
held fixed. Trajectories that end this way show a significantly higher share
of "struggle" steps (repeated failed attempts, no progress) — the abandonment
is semantically observable BEFORE it happens.

## What ggcode does

`internal/agent/premature_termination_guard.go` combines the two signals into
a predictive, pre-surrender advisory:

- **Struggle density**: rolling window (12 steps) of tool-step error flags;
  density = errored steps / filled window. Fires only from 8 filled steps.
- **Context occupancy**: `context.Manager.UsageRatio()`, the paper's core
  variable.
- Both over threshold (density >= 0.5, occupancy >= 60%) -> inject ONCE per
  run: "[Continuation Advisory] ... N% of the context window is still
  available - do not wrap up or give up yet ..." plus a three-step
  re-engagement structure (re-read last error; name the disproved
  assumption; pick a different strategy shape).

Deterministic and zero-LLM-cost. State resets per user turn (same contract as
other run-scoped detectors).

## Distinct from existing detectors

| Detector | Signal | Timing |
| --- | --- | --- |
| giveup_revert | give-up wording + tree rollback | after the fact |
| futile_cycle | duplicate read similarity | single signal |
| iter_pressure | iteration budget x verify-rate drop | different axis (iterations, not context tokens) |
| **termGuard (this)** | **struggle density x context occupancy** | **before surrender is drafted** |

## Thresholds

| Constant | Value | Rationale |
| --- | --- | --- |
| ptGuardWindow | 12 | recent-work window, cheap ring |
| ptGuardMinSteps | 8 | statistical floor before density means anything |
| ptGuardStruggleDensity | 0.5 | half of recent steps failing = struggle-heavy |
| ptGuardOccupancy | 0.6 | paper: risk grows with length; 40% headroom still substantial |
