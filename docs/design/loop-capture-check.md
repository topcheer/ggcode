# Range/For Loop Variable Capture Detection

## Intelligence Check: loop-capture (Check #59)

### Problem

AI coding agents frequently produce Go code with loop variable capture bugs - one of the most classic and dangerous Go pitfalls. Before Go 1.22, loop variables (both `for i := 0` and `for _, v := range`) are shared across all iterations. Capturing them in a closure launched via `go func()` or `defer func()` causes all closures to see the last value.

```go
for _, item := range items {
    go func() {
        process(item) // BUG: all goroutines see the last item
    }()
}
```

Even in Go 1.22+ (where range loop vars are per-iteration), the `for i := 0` pattern still has the shared-variable issue when targeting older Go versions. Code may also be ported to pre-1.22 environments.

### Detection Patterns

| Pattern | Example | Flagged? |
|---------|---------|----------|
| Range var in goroutine closure | `for _, v := range s { go func() { use(v) }() }` | Yes |
| Range var in defer closure | `for _, v := range s { defer func() { use(v) }() }` | Yes |
| For-init var in goroutine | `for i := 0; i < n; i++ { go func() { use(i) }() }` | Yes |
| Key var in goroutine | `for i := range s { go func() { use(i) }() }` | Yes |
| Passed as parameter | `for _, v := range s { go func(v T) { use(v) }(v) }` | No (safe) |
| Rebound before use | `for _, v := range s { v := v; go func() { use(v) }() }` | No (safe) |
| Closure not referencing loop var | `for _, v := range s { go func() { use(x) }() }` | No (safe) |

### Competitor Analysis

| Competitor | Write-time detection? |
|------------|----------------------|
| Claude Code | No |
| Cursor | No |
| Cline/OpenHands | No |
| Aider | No |
| Devin | No |
| go vet | No (no closure capture analysis) |
| staticcheck | No (S1011 is unrelated) |

### Implementation

- **File**: `internal/agent/loop_capture_check.go` (287 lines)
- **Test**: `internal/agent/loop_capture_check_test.go` (231 lines, 12 tests)
- **Registration**: 1 line in `write_integrity.go` (`loop-capture`)
- **Language**: Go only
- **Cost**: Zero LLM cost (pure AST analysis)
- **Max cyclomatic complexity**: Under 15 for all functions
- **Approach**: AST-based with delta-aware suppression (only flags newly introduced patterns)
- **Warnings capped at 3 per file**
