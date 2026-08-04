# Concurrency Race Detection Verification Hint (Post-Write Check)

## Problem

Data races are the #1 source of subtle, non-deterministic bugs in concurrent
Go programs. They occur when two or more goroutines access the same variable
concurrently, and at least one of the accesses is a write. Data races are
**invisible to static analysis** -- only the Go race detector
(`go test -race`) catches them at runtime.

In practice, data races manifest as:
- Flaky tests that pass locally but fail in CI
- Non-deterministic corruption of shared state
- Heisenbugs that disappear when debugging is added
- Production incidents under high concurrency

## Gap in Existing Checks

ggcode has extensive static concurrency analysis:

- `goroutine-leak-check.go`: detects goroutines without lifecycle management
- `lock-without-unlock-check.go`: detects mutex deadlocks
- `concurrent-map-access.go`: detects unguarded map access
- `select-timer-leak.go`: detects leaked timer goroutines

However, **none of these checks proactively suggests runtime verification**.
Static analysis catches structural issues; the race detector catches temporal
issues (concurrent read/write without synchronization). They are complementary
but neither substitutes for the other.

An agent can write code that passes all static checks yet contains a data
race that only manifests under `go test -race`.

## Competitor Analysis

- **Claude Code**: no automatic `-race` suggestion
- **Cursor**: no automatic `-race` suggestion
- **Cline/OpenHands**: no automatic `-race` suggestion
- **Aider**: no automatic `-race` suggestion
- **GitHub Copilot**: sometimes mentions `-race` in chat responses, no
  automated trigger based on code changes

## Design

### Detection Targets

The check uses AST-based analysis to detect **newly introduced** concurrency
primitives in Go source files:

1. **`go` statements** -- goroutine launches (primary race risk)
2. **`sync.Mutex` / `sync.RWMutex`** -- shared state protection
3. **`sync.WaitGroup`** -- goroutine coordination
4. **`sync.Map`** -- concurrent map access
5. **`sync.Once`** -- one-time initialization
6. **`sync.Cond`** -- condition variables
7. **`sync.Pool`** -- object pooling
8. **`atomic.*` operations** -- lock-free concurrency
9. **Channel sends** -- `ch <- value` (can race with concurrent receivers)

### Delta-Awareness

The check only fires when **new** concurrency primitives are introduced or
modified. If the old content already had the same concurrency patterns, no
hint is generated. This avoids noise on reformats, whitespace edits, or
unrelated changes.

### Deliberate Exclusions

- **Test files** (`*_test.go`): these already run under `go test`, and the
  hint is about verifying the production code they test.
- **Non-Go files**: the race detector is Go-specific.
- **`context.WithCancel` / `context.WithTimeout`**: these are for
  cancellation, not races.
- **`time.After` / `time.Timer`**: single-goroutine timing.

### Zero LLM Cost

The check is purely deterministic (AST parsing + string matching). No API
calls, no latency, no token consumption. It produces a concise, actionable
hint appended to the tool result when a concurrency edit is detected.

## Implementation

- **File**: `internal/agent/race_verify_hint.go`
- **Registration**: `internal/agent/write_integrity.go` (in `registerAllChecks`)
- **Tests**: `internal/agent/race_verify_hint_test.go` (13 tests)

## Verification

When the check fires, it appends this guidance to the tool result:

> Concurrency code modified -- data races are invisible to static analysis.
> Verify with `go test -race ./...` to catch concurrent read/write hazards
> that compile fine but fail non-deterministically at runtime.

This is informational and non-blocking -- the edit still applies normally.
