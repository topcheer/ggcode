# Ignored Error from Close() Detection

**Check ID**: close-error-ignored
**Language**: Go
**Category**: Resource Safety / Error Handling

## Problem

In Go, `defer file.Close()` silently discards the error returned by `Close()`.
For read-only files this is usually harmless, but for writable handles (files
opened with `os.Create`, `os.OpenFile` with `O_WRONLY`/`O_RDWR`, `gzip.Writer`,
`bufio.Writer`, etc.), the Close error may indicate:

- **Failed flush**: buffered data could not be written to disk
- **ENOSPC**: disk full during final write-back
- **Network errors**: for connection-based writers

This leads to **silent data loss** - the program thinks it succeeded but the
data was never fully written.

## Detection

The check scans for `defer <expr>.Close()` statements that are:
1. Direct defer of a method call (not wrapped in a closure)
2. The method is named `Close` (satisfies `io.Closer` interface)
3. Takes no arguments

### False Positive Mitigation

- **Closure-wrapped**: `defer func() { if err := f.Close(); ... }()` is correctly skipped
- **Close with args**: Methods like `Close(ctx)` are skipped (only zero-arg Close flagged)
- **Non-Close methods**: Only the exact name `Close` triggers the warning

## Recommended Fix

```go
// Before (error lost):
defer file.Close()

// After (error handled):
defer func() {
    if err := file.Close(); err != nil {
        log.Printf("close failed: %v", err)
    }
}()
```

## Competitor Analysis

| Tool | Write-time detection | Lint-time detection |
|------|---------------------|--------------------|
| Claude Code | No | No |
| Cursor | No | No (relies on external linters) |
| OpenHands | No | No |
| Aider | No | No |
| golangci-lint (errcheck) | No | Yes (but only if configured) |
| staticcheck | No | No (does not flag defer .Close()) |
| **ggcode** | **Yes** | **Yes** |

## Implementation

- **File**: `internal/agent/close_error_check.go`
- **Registration**: `write_integrity.go` (close-error-ignored check)
- **Complexity**: All functions under cyclomatic complexity 15
- **LLM cost**: Zero — pure AST pattern matching
- **Dependencies**: Go standard library only

## Test Coverage

10 test cases covering:
- Basic `defer file.Close()` detection
- Closure-wrapped Close (no false positive)
- Non-Close defer methods
- Chained receiver Close (e.g., `os.Stdout.Close()`)
- Multiple Close() in same function
- Close with arguments (no false positive)
- Non-Go file filtering
- Empty content handling
- Syntax error resilience
- Warning truncation at maxCloseErrWarnings
