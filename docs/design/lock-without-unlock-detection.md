# Mutex Lock-Without-Unlock Detection (Post-Write Check)

## Problem

AI coding agents frequently produce Go code that calls `mu.Lock()` or
`mu.RLock()` but forgets the corresponding `Unlock()`/`RUnlock()`. This
causes permanent deadlocks -- the goroutine holds the lock forever, blocking
all other goroutines that try to acquire it.

Unlike resource leaks (missing `Close()` on files), deadlocks are harder to
diagnose because they manifest as hangs rather than errors. Tests may pass
if they don't exercise concurrent paths.

## Gap in Existing Checks

The existing `resource_leak_check.go` lists "Unlock"/"RUnlock" as cleanup
methods, but its detection logic only matches resource-acquiring assignments
(e.g., `f, err := os.Open()`). A mutex `Lock()` call is a bare statement
(`mu.Lock()`), not an assignment -- so missing Unlock was **never detected**
by the existing check.

## Competitor Analysis

- **Claude Code**: no automatic detection (relies on external linters)
- **Cursor**: no automatic detection (go vet doesn't catch this)
- **Cline/OpenHands**: reactive only -- caught by tests or production deadlocks
- **Aider**: no automatic detection
- **Windsurf**: no automatic detection

`go vet`'s `-copylocks` check catches lock-by-value but NOT missing-unlock.
`staticcheck` doesn't have a rule for this. `go-deadlock` (external tool)
detects runtime deadlocks but requires the deadlock to actually occur.

## Approach

AST-based analysis of Go functions. For each function:

1. Find all `Lock`/`RLock`/`TryLock` calls and their receiver expressions
2. Find all `Unlock`/`RUnlock` calls (direct or via `defer`) and their receivers
3. If any lock receiver has no matching unlock, emit a warning

The receiver matching handles:
- Simple identifiers: `mu.Lock()` / `mu.Unlock()`
- Struct field selectors: `s.mu.Lock()` / `s.mu.Unlock()`
- Indexed expressions: `m["key"].Lock()` / `m[...].Unlock()`

Only **new** instances introduced by this edit are flagged (delta-aware),
using the same pattern as other post-write checks: count old instances,
flag only the excess in new content.

## Detection Examples

### Detected: Lock without Unlock

```go
func worker(mu *sync.Mutex) {
    mu.Lock()
    doWork()
    // Warning: no defer mu.Unlock() -- permanent deadlock
}
```

### OK: Lock with defer Unlock

```go
func worker(mu *sync.Mutex) {
    mu.Lock()
    defer mu.Unlock()
    doWork()
}
```

### Detected: TryLock without Unlock

```go
func worker(mu *sync.Mutex) {
    if mu.TryLock() {
        doWork()
        // Warning: no mu.Unlock() inside the if block
    }
}
```

## Implementation

- **File**: `internal/agent/lock_without_unlock_check.go`
- **Registration**: `internal/agent/write_integrity.go` (check #24)
- **Test**: `internal/agent/lock_without_unlock_check_test.go` (16 tests)
- **Cost**: Zero LLM cost, <1ms per file (pure AST analysis)
- **Dependencies**: None (uses `go/ast` from standard library)
