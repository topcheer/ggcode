# Resource Leak Detection (Post-Write Check)

## Problem

AI coding agents frequently produce Go code that acquires resources -- file
handles, HTTP response bodies, network listeners -- without the corresponding
`defer Close()` cleanup call. This causes:

- File descriptor exhaustion
- Goroutine leaks
- Memory pressure
- Intermittent production failures

LLMs are especially prone to this because they focus on the happy-path logic
and frequently omit the `defer` statement.

## Competitor Analysis

| Tool | Detection | Timing |
|------|-----------|--------|
| Claude Code | None (relies on external linters) | N/A |
| Cursor | None (lint-on-save may catch via golangci-lint) | Reactive |
| Cline/OpenHands | None | Reactive (tests/incidents) |
| Aider | None | N/A |
| Windsurf | None | N/A |
| **ggcode** | **AST-based inline detection** | **At write time (<1ms)** |

External tools (staticcheck, errcheck, revive) can catch some of these, but
require a separate lint cycle and are not always installed. This check provides
immediate, zero-dependency feedback using Go's standard library AST parser.

## Design

The check runs as part of the `checkWriteIntegrity` pipeline (check #17) after
every successful file write. It:

1. Parses the file using `go/parser` (the AST is already parsed for other checks)
2. For each function, finds resource-acquiring call patterns
3. For each acquisition, verifies a corresponding cleanup call exists
4. Emits warnings with the variable name and position

### Detected Patterns

| Package | Functions | Resource | Cleanup Target |
|---------|-----------|----------|----------------|
| `os` | `Open`, `Create`, `OpenFile` | File handle | `defer f.Close()` |
| `net` | `Listen` | Network listener | `defer l.Close()` |
| `http` | `Get`, `Post`, `Head`, `PostForm` | HTTP response body | `defer resp.Body.Close()` |

### Cleanup Method Recognition

The check recognizes these method names as cleanup: `Close`, `CloseAll`,
`Cleanup`, `Release`, `Free`, `Shutdown`, `Stop`, `Unlock`, `RUnlock`.

Only methods matching these names are counted as cleanup, preventing false
negatives where `f.Read()` would incorrectly satisfy the cleanup requirement
for a file handle.

### Limitations

- **Go files only**: Uses `go/parser` which only parses Go source.
- **Function-scoped**: Cleanup must be in the same function body as the
  acquisition. Cross-function cleanup (e.g., passing the resource to a
  closer goroutine) is not recognized.
- **Mutex lock/unlock**: Intentionally not checked because lock patterns are
  highly variable (conditional unlocks, RWLock with RLock) and would generate
  excessive false positives.
- **Custom resource types**: Only standard library patterns are checked.
  Custom resource types with different method names are not covered.

## Files

- `internal/agent/resource_leak_check.go` -- Detection logic
- `internal/agent/resource_leak_check_test.go` -- 13 test cases
- `internal/agent/write_integrity.go` -- Pipeline integration (check #17)
