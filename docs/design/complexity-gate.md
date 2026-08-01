# Post-Completion Complexity Regression Gate

## Problem

AI coding agents can introduce high-complexity code that compiles and passes tests but is difficult to maintain. The existing `codehealth.Analyze()` function was available as a manual tool but was never automatically invoked during the agent's completion flow. This meant the agent could declare "done" after writing functions with cyclomatic complexity > 30, deep nesting, or excessive length without any quality advisory.

## Competitive Analysis

| Competitor | Approach | Gap |
|---|---|---|
| **Devin (SICA)** | LLM-based overseer reviews code quality | Expensive (extra LLM calls), latency |
| **Cursor** | In-process lint-on-save | Catches style, not semantic complexity |
| **Claude Code** | Self-judgment only | Unreliable — agent won't flag its own code |
| **Aider** | Diff review before commit | Manual, doesn't run automatically |
| **Cline/OpenHands** | Reactive (build/test only) | Complex code compiles fine |

## Solution

A deterministic, zero-LLM-cost complexity gate that runs after build verification passes:

1. **Trigger**: After sync verify passes (build is clean) and before the agent returns completion
2. **Scope**: Only scans `.go` files the agent actually edited in this run
3. **Analysis**: Uses `codehealth.Analyze()` (Go AST parser) to compute cyclomatic complexity, function length, and nesting depth
4. **Thresholds**: Flags functions with complexity >= 15, length > 80 lines, or nesting > 5 levels
5. **Advisory**: Injects a quality advisory message into the agent context, giving it a chance to refactor before completing
6. **Bounded**: Fires at most once per run, caps reported functions at 5

### Why These Thresholds?

- **Complexity >= 15**: Codehealth classifies 11-19 as "medium" and 20+ as "high". We chose 15 to catch genuinely problematic functions while avoiding false positives on moderate business logic.
- **Length > 80**: Functions exceeding 80 lines typically indicate multiple responsibilities.
- **Nesting > 5**: Deep nesting is a strong readability predictor and a common refactoring signal.

### Integration Points

- **Reset**: Gate state resets at the start of each user turn (alongside fulfillment gate, loop detector, etc.)
- **Completion path**: Runs after `syncVerifyAndGate` passes, before the agent returns
- **Non-blocking**: Advisory only — doesn't prevent completion if the agent chooses not to refactor

## Files

- `internal/agent/complexity_gate.go` — Gate implementation
- `internal/agent/complexity_gate_test.go` — Tests
- `internal/agent/agent.go` — Struct field, init, reset, and completion path integration
- `internal/codehealth/complexity.go` — Existing analysis engine (unchanged)
