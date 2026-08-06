# Init Function with Side Effects Detection

## Problem

Go's `init()` functions run at package import time, before `main()`. AI coding agents sometimes place I/O operations, network calls, environment mutations, goroutine launches, or process termination calls inside `init()`, which:

1. Makes the package unimportable in test environments without those resources
2. Causes import-time panics that are hard to debug
3. Hides side effects from the import graph
4. Makes the code untestable -- callers cannot control when side effects happen

## Anti-Patterns Detected

```go
func init() {
    data, _ := os.ReadFile("config.json")   // file I/O at import time
    http.Get("http://example.com/health")     // network I/O at import time
    go startServer()                           // goroutine at import time
    os.Setenv("KEY", "val")                    // env mutation at import time
    log.Fatal("cannot proceed")                // terminates process at import
}
```

## Approach

AST-based analysis of `init()` function bodies. For each `init()` FuncDecl:

1. Walk all AST nodes in the body
2. Flag `GoStmt` nodes (goroutine launches)
3. Flag `CallExpr` with `SelectorExpr` where:
   - The function name is `Fatal`, `Fatalf`, `Fatalln`, `Panic`, `Panicf`, `Panicln`, or `Exit`
   - The package is `os`, `http`, `ioutil`, `fmt`, `log`, or `time`

## Competitor Analysis

- Claude Code, Cursor, OpenHands, Aider: no write-time detection
- golangci-lint: `gochecknoinits` flags ALL init() but not specific side effects
- staticcheck: does not check init() side effects

## Implementation

- **File**: `internal/agent/init_sideeffect_check.go`
- **Test**: `internal/agent/init_sideeffect_check_test.go` (13 tests)
- **Registration**: `write_integrity.go` as `init-side-effects` check
- **Helper prefix**: `ise` (to avoid naming collisions)
- **Max warnings**: 5 (with truncation notice)
- **Zero LLM cost**: pure AST pattern matching
