# Deep Nesting Detection Check

## Overview

**Check name:** `nesting-depth`
**File:** `internal/agent/nesting_depth_check.go`
**Languages:** Go
**Registration:** `write_integrity.go` -> `allChecks`
**Cost:** Zero LLM (pure AST analysis, <1ms)

## Problem

Cognitive Complexity research (SonarSource, 2018; adopted by SonarQube, GitHub CodeQL by 2025) identifies nesting depth as the single strongest predictor of code comprehension difficulty. Each additional nesting level forces a developer to hold an additional context frame, increasing cognitive load exponentially.

AI coding agents are particularly prone to generating deeply nested code:
- They append logic inside existing `if`/`else`/`for` blocks rather than refactoring to flat structure.
- They rarely use guard clauses (early returns) proactively.
- They nest error handling, feature flags, and validation checks without flattening.

**No competitor detects this at write time:**
| Tool | Write-time detection | Notes |
|------|-------------------|-------|
| Claude Code | No | Relies on agent self-judgment |
| Cursor | No | Lint-on-save catches JS complexity only |
| Cline/OpenHands | No | Reactive (caught by build/test cycle) |
| Devin | No | Post-completion review only |
| SonarQube | No | CI pipeline only, not write-time |
| Aider | No | Commits per-edit, visible in diff review |

## Detection Approach

### What counts as nesting

Control-flow statements that create a new indentation/block level:
- `if` / `else if` / `else`
- `for` (C-style)
- `for range`
- `switch`
- `type switch`
- `select`

### Key design decisions

1. **Else-if chains are flat**: `if a {} else if b {} else if c {}` counts as depth 1, not depth 3. This is because else-if represents a single decision level (a flat dispatch), not nested decisions.

2. **Bare blocks don't increase depth**: Go allows bare `{ }` blocks for scoping. These are structural, not control-flow. Only control-flow statements increase depth.

3. **Case clauses share the switch's depth**: Statements inside a `case` body are at the same depth as the switch statement itself (switch=1, case body statements start at depth 1).

4. **Threshold: depth > 4** (i.e., warns at 5+ levels). Aligns with SonarQube's default nesting depth threshold.

5. **Labels are transparent**: A labeled statement (`label: for {}`) is walked through to the underlying statement without affecting depth.

### Delta-aware

Only flags nesting that is:
- **Newly introduced** (function didn't exist before)
- **Worsened** (function's max depth increased beyond previous level)

Pre-existing deep nesting that was already in the file is NOT flagged, avoiding noise on large legacy files.

## Warning Format

```
processData has deep control-flow nesting (depth 5, recommended <= 4) - consider extracting nested logic into helper functions or using guard clauses (early return)
```

Warnings are capped at 3 per file, with a summary for additional violations.

## Complexity

All functions maintain cyclomatic complexity under 15:
- `checkNestingDepth`: ~8
- `walkNesting`: ~10
- `walkElseChain`: ~4
- `walkBlockStmt`: ~1
- `walkCaseClauses`: ~2
- `walkCommClauses`: ~2

## Test Coverage

18 test cases covering:
- Shallow nesting (no warning)
- Deep nesting at various depths
- Else-if chain flatness
- Else-if body with deep nesting
- Switch/type-switch/select nesting
- Labeled statements
- Bare blocks
- Boundary condition (depth 4 = no warning)
- Delta-aware: pre-existing not flagged
- Delta-aware: worsened nesting flagged
- Multiple functions capped at 3 warnings
- Non-Go files skipped
- Empty content skipped
- Parse errors return nil
