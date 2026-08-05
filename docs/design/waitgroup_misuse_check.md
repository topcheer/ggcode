# WaitGroup Misuse Detection (Write-Time Intelligence Check #53)

## Trend Direction
**Concurrency Safety** -- one of the hottest topics in AI Agent reliability.

## Problem
AI coding agents frequently produce Go code with `sync.WaitGroup` misuse
patterns that cause runtime panics or deadlocks. These bugs compile fine and
may pass basic tests, manifesting only in production under load.

### Three Misuse Patterns Detected

| # | Pattern | Consequence | Root Cause |
|---|---------|-------------|------------|
| 1 | `wg.Done()` without `defer` | `Wait()` hangs forever on early return/panic | Any code path between Add and Done that exits early skips the decrement |
| 2 | `wg.Done()` but no `wg.Add()` | Immediate return (race) + panic on Done | Counter stays at 0; Done decrements to -1 |
| 3 | `wg.Add(1)` inside goroutine | `Wait()` returns prematurely | Add may not execute before Wait is called |

## Gap Analysis

### Existing ggcode Coverage (not overlapping)
- `goroutine_leak_check.go` -- detects goroutines with NO sync at all
- `lock_without_unlock_check.go` -- detects mutex deadlocks
- `context_leak_check.go` -- detects context.TODO/Background misuse
- `select_timeout_leak_check.go` -- detects time.After in select
- `defer_cancel_check.go` -- detects missing defer on context cancel

**Gap**: No check detects INCORRECT WaitGroup usage. The goroutine-leak
check skips functions that contain any WaitGroup reference (treating it as
"synchronized"), so misused WaitGroups fall through undetected.

### Competitor Analysis

| Competitor | Detection | Notes |
|-----------|-----------|-------|
| Claude Code | None | Relies on agent judgment |
| Cursor | None | go vet doesn't cover WaitGroup misuse |
| Cline/OpenHands | None | Reactive only (production incidents) |
| Aider | None | No static analysis |
| Devin | None | |
| GitHub Copilot | Partial | Sometimes suggests correct patterns in completions, but doesn't verify edits |

**go vet** does NOT detect any of these patterns.
**staticcheck** has no WaitGroup misuse rule.
**go-deadlock** detects runtime deadlocks but requires the deadlock to occur.

## Implementation

**File**: `internal/agent/waitgroup_check.go` (243 lines)
**Tests**: `internal/agent/waitgroup_check_test.go` (15 tests, all passing)
**Registration**: 1 line in `write_integrity.go` → `allChecks`

### Approach
AST-based analysis, zero LLM cost. For each function body:
1. Collect WaitGroup method call statistics via `collectWGStats()`
2. Identify bare vs deferred Done(), total Add(), Add() inside goroutines
3. Check three patterns via `analyzeWGFunc()`

### Design Decisions
- **Fast path**: `strings.Contains(src, "WaitGroup")` skips files with no
  WaitGroup reference entirely -- zero parse cost for 99% of files
- **Delta-aware**: only flags issues NEWLY introduced by this edit
- **Skips test files**: WaitGroup patterns in tests are common (parallel tests)
- **Max 3 warnings per write**: avoids overwhelming the agent
- **Method-name based**: detects any `*.Done()`, `*.Add()`, `*.Wait()` selector
  calls regardless of variable name, matching real-world usage
- **Cyclomatic complexity**: all functions under 15

### Cyclomatic Complexity
| Function | Complexity |
|----------|-----------|
| `checkWaitGroupMisuse` | 6 |
| `findWaitGroupMisuse` | 4 |
| `analyzeWGFunc` | 7 |
| `collectWGStats` | 4 |
| `goroutineHasWGAdd` | 3 |

## Verification
- `go build -tags goolm ./...` -- passes
- 15/15 unit tests pass
- No external dependencies added
- No existing code behavior modified
