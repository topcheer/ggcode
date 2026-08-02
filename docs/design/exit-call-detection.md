# Premature Exit Call Detection

## Problem

AI coding agents frequently produce Go code that calls `os.Exit()`, `log.Fatal()`,
`log.Fatalf()`, `log.Fatalln()`, `log.Panic()`, `log.Panicf()`, or `log.Panicln()`
inside helper functions, middleware, library packages, or service handlers.

These calls are problematic because they:

1. **Skip all deferred functions** - `defer Body.Close()`, `defer Unlock()`, etc.
   never execute, causing resource leaks, unflushed buffers, and data corruption.
2. **Make functions untestable** - the test process is killed, making it impossible
   to assert error behavior in unit tests.
3. **Prevent callers from handling errors** - violates the Go convention of
   returning errors so callers can decide what to do.
4. **Crash long-running services** - in HTTP servers, workers, or daemons, a single
   bad request or edge case can bring down the entire process.

## Correct Usage

`os.Exit()`, `log.Fatal*()`, and `log.Panic*()` should only appear in:

- `main()` functions (top-level program bootstrap)
- `init()` functions (package initialization)
- `cmd/` binary entry points
- `TestMain()` in `_test.go` files (test harness entry point)

In all other functions, the correct pattern is to **return an error**:

```go
// Bad
func loadConfig() *Config {
    data, err := os.ReadFile("config.yaml")
    if err != nil {
        log.Fatal(err) // kills process, skips defers, untestable
    }
    // ...
}

// Good
func loadConfig() (*Config, error) {
    data, err := os.ReadFile("config.yaml")
    if err != nil {
        return nil, fmt.Errorf("read config: %w", err)
    }
    // ...
}
```

## Competitor Analysis

| Tool | Detection | Mechanism |
|------|-----------|-----------|
| Claude Code | None | Relies on external linters |
| Cursor | None at write time | Lint-on-save via golangci-lint |
| Cline/OpenHands | Reactive only | Caught by tests or production |
| Aider | None | - |
| Windsurf | None | - |
| go vet | Does not flag | - |
| staticcheck | Does not flag | - |
| gosec | Does not flag | - |

**No competitor provides inline detection at write time.** ggcode's check
provides immediate, zero-dependency feedback in <1ms per file using Go's
standard library AST parser.

## Implementation

- **File**: `internal/agent/exit_call_check.go`
- **Registration**: `checkWriteIntegrity()` in `internal/agent/write_integrity.go` (check #23)
- **Approach**: AST-based analysis. Walks all function declarations and function
  literals (closures) in a file. For each function not named `main` or `init`,
  scans its body (recursively through nested blocks, closures, if/switch/for/select)
  for calls to `os.Exit`, `log.Fatal/Fatalf/Fatalln`, or `log.Panic/Panicf/Panicln`.
- **Exclusions**: `_test.go` files, `cmd/` directories, `main()` and `init()` functions.
- **Delta-aware**: Only flags patterns newly introduced by the current edit.
  Pre-existing calls are subtracted using per-function-name counting.
