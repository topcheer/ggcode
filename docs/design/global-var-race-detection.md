# Global Variable Race Detection (Check #75)

## Problem

AI coding agents frequently produce Go code that mutates package-level
(global) variables inside goroutines without synchronization. Unlike local
variables, package-level variables are shared across all goroutines. Writing
to them from a goroutine without a mutex, atomic operation, or channel creates
a data race.

Go's built-in race detector (`go test -race`) can catch these at test time,
but:
1. It requires tests that actually exercise the concurrent path.
2. It only detects races that manifest during the test run.
3. It provides no feedback at write time.

The existing `concurrent-map-access` check only covers map types. This check
extends coverage to ALL package-level variables mutated inside goroutine
function literals.

## Approach

Pure AST-based analysis using Go's standard library parser. For each Go file:

1. **Collect package-level variable names** from top-level `var` declarations.
2. **Find goroutine launches** (`go func() { ... }(...)` statements).
3. **Check for synchronization** in the goroutine body (mutex Lock/Unlock,
   atomic Store/Load/Add/Swap, etc.).
4. **Find assignments to globals** inside the goroutine body.
5. **Warn** if a global is mutated without visible synchronization.

## Detection Patterns

### Detected (Warning)
```go
var counter int

go func() {
    counter = 42  // WARNING: data race
}()
```

```go
var count int

go func() {
    count += 1  // WARNING: compound assignment also flagged
}()
```

### Suppressed (No Warning)
```go
var (
    counter int
    mu      sync.Mutex
)

go func() {
    mu.Lock()
    defer mu.Unlock()
    counter = 42  // OK: mutex-protected
}()
```

```go
var counter int64

go func() {
    atomic.StoreInt64(&counter, 42)  // OK: atomic-protected
}()
```

## Design Decisions

- **Only checks `go func() { ... }()` literals**, not `go namedFunc()`. This
  is conservative — we can't trace cross-function data flow at write time.
- **Any sync call in the goroutine body suppresses ALL warnings** for that
  body. This is conservative (may miss some races) but minimizes false
  positives.
- **Only unqualified identifiers** are checked (e.g., `counter`, not
  `pkg.Counter`). Cross-package global references are skipped.
- **Capped at 4 warnings** per file with truncation notice.
- **Helper functions prefixed with `gvr`** to avoid naming collisions.
- **Zero LLM cost** — pure AST pattern matching.

## Competitor Analysis

| Agent | Write-time Detection |
|-------|---------------------|
| Claude Code | No |
| Cursor | No (go vet -race post-save) |
| Cline/OpenHands | No |
| Aider | No |
| Windsurf | No |
| GitHub Copilot | No |

No major AI coding agent detects global variable data races at write time.

## Files

- `internal/agent/global_var_race_check.go` — check implementation
- `internal/agent/global_var_race_check_test.go` — 13 tests
- `internal/agent/write_integrity.go` — registration (1 entry)
