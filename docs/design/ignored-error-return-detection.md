# Ignored Error Return Detection

## Problem

AI coding agents frequently produce Go code that calls functions returning an
error value but completely ignores (discards) the error. This is the most
fundamental error-handling anti-pattern in Go and is distinct from the patterns
covered by `error-swallow-detection.md` (which only detects `if err != nil {}`
empty handlers and bare returns).

### Patterns Detected

1. **Completely ignored call**: A function call whose last return type is error,
   where the call appears as a standalone statement (not assigned to anything).
   Example: `json.Marshal(data)` - the error is silently dropped.

2. **Explicitly discarded error**: `_ = someFunc()` or `_, _ = someFunc()`
   where the error return is assigned to the blank identifier.

### Why It Matters

- The code compiles and appears correct
- Errors from I/O (file, network), parsing (JSON, XML), and state mutations
  are silently lost
- It's the #1 issue flagged by `errcheck` and `staticcheck SA4006`
- `go vet` does NOT catch this pattern

## Competitor Analysis

| Tool | Detection |
|------|-----------|
| Claude Code | No automatic detection (relies on external linters) |
| Cursor | Lint-on-save may catch via errcheck, not at write time |
| Cline/OpenHands | Reactive only - caught by tests or production incidents |
| Aider | No automatic detection |
| Windsurf | No automatic detection |
| errcheck | Catches this but requires installation and separate lint cycle |
| staticcheck SA4006 | Catches unused assignments, not bare calls |

None provide inline detection at write time.

## Implementation

**File**: `internal/agent/ignored_error_check.go`

The check uses AST-based analysis with two strategies:

1. **Curated function map**: A map of known stdlib functions/methods that return
   error (60+ entries covering fmt, encoding/json, encoding/xml, os, io, http,
   bufio, database/sql, strconv, template, exec, etc.)

2. **Method name heuristic**: For method calls on local variables where the
   receiver type cannot be resolved statically (e.g., `f.Close()` where `f` is
   a local `*os.File`), the check falls back to matching against a set of
   method names commonly associated with error returns (Write, Close, Encode,
   Flush, Sync, Marshal, etc.)

### Constructor Resolution

Method chains like `json.NewEncoder(w).Encode(data)` are resolved via a
`constructorReturns` map that maps constructors to their return types:
`json.NewEncoder` -> `json.Encoder`, so the chain resolves to
`json.Encoder.Encode`.

### Delta-Aware

Only flags patterns newly introduced by the current edit (same approach as all
other post-write checks).

## Test Coverage

10 test cases covering:
- Standalone ignored calls (`json.Marshal(data)`)
- Explicit discard (`_ = json.Marshal(data)`)
- Properly checked errors (no false positives)
- Non-Go files (no warnings)
- Delta-aware detection
- Method chain resolution (`json.NewEncoder(w).Encode(data)`)
- Non-error functions (no false positives)
- Integration via `checkWriteIntegrity`
- Method name heuristic on local variables
