# Error Order Detection

## Problem

AI coding agents frequently produce Go code that uses the result of an error-returning function call before checking the error. The classic pattern:

```go
resp, err := http.Get(url)
defer resp.Body.Close()  // BUG: if err != nil, resp is nil -> panic
if err != nil {
    return err
}
```

When `err` is non-nil, the result variable (`resp`) is typically nil or in an
invalid state. Using it before the error check causes nil pointer dereference
panics. The `defer` statement executes at function return regardless of error,
so even after the error check returns, the deferred call still runs on nil.

## Competitor Analysis

| Agent            | Detection                                     |
|------------------|-----------------------------------------------|
| Claude Code      | None (relies on external linters)             |
| Cursor           | None (lint-on-save may catch via staticcheck) |
| Cline/OpenHands  | Reactive only (caught by tests/incidents)     |
| Aider            | None                                          |
| Windsurf         | None                                          |

External tools like `staticcheck` and `go vet` can catch some of these, but
require a separate lint cycle and are not always installed. This check
provides immediate, zero-dependency feedback at write time.

## Approach

AST-based analysis of Go function bodies. For each assignment matching the
pattern `result, err := f(...)`, the check scans forward in the same block for
the first error check (`if err != nil`). If the result variable is used in any
statement between the assignment and the error check (including `defer`), a
warning is emitted.

Key design decisions:

1. **Delta-aware**: Only flags NEW instances introduced by this edit, avoiding
   noise on pre-existing code.
2. **Same-block only**: Only flags when the error IS checked later in the same
   block. If the error is never checked in the block, the pattern is not
   flagged (the error may be handled by the caller).
3. **Recursive**: Descends into nested blocks (if/for/range/switch/select).
4. **Blank result safe**: `_`-assigned results are not tracked.

## Detection Scope

Detected patterns:
- `defer resp.Body.Close()` before `if err != nil` (most common)
- Direct use like `print(val)` before error check
- Method calls on result before error check
- Nested blocks with the same pattern

Not flagged:
- Result used after the error check (correct order)
- Error never checked in the same block (may be intentional)
- Blank-identifier results (`_, err := f()`)
- Non-Go files

## Implementation

- File: `internal/agent/error_order_check.go`
- Registered as check #26 in `internal/agent/write_integrity.go`
- Tests: `internal/agent/error_order_check_test.go` (11 test cases)
- Zero external dependencies (uses Go stdlib `go/ast`, `go/parser`, `go/token`)
