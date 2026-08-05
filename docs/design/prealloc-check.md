# Preallocation Detection (Check #56)

## Performance & Latency Optimization: Missing Preallocation Detection

### Problem
AI coding agents frequently generate Go code that appends to slices inside loops
without preallocating capacity. Each `append` beyond capacity triggers a full
copy (O(n) allocation + memcpy), so N iterations cause O(N log N) total
allocations and copies. For N=10,000, this means ~14 reallocation events and
significant GC pressure.

### Detection
**AST-based, delta-aware, zero LLM cost.** Scans for:
- `var x []T` followed by `x = append(x, ...)` inside `for`/`range` loops
- `x := []T{}` followed by `x = append(x, ...)` inside loops

### What it catches
```go
// BAD - triggers warning
var results []int
for _, item := range items {
    results = append(results, process(item))
}

// GOOD - no warning (preallocated)
results := make([]int, 0, len(items))
for _, item := range items {
    results = append(results, process(item))
}
```

### What it skips
- Preallocated slices: `make([]T, 0, n)` or `make([]T, n)`
- Function-return slices: `parts := strings.Split(...)` (not zero-cap from user code)
- Test files (`_test.go`) - small slices, not worth flagging
- Non-Go files
- Files with syntax errors (handled by other checks)
- Delta-aware: only flags new patterns, not pre-existing ones

### Competitor analysis
| Tool | Detection |
|------|-----------|
| Claude Code | No |
| Cursor | Relies on staticcheck (SA1024 only catches `make([]T, 0)` vs `make([]T, 0, 0)`) |
| Cline/OpenHands | No |
| Aider | No |
| go vet | No |
| staticcheck | No (not this pattern) |
| prealloc linter | Yes, but NOT in default toolchains |
| gocritic | Yes, but rarely configured |

**ggcode is the first AI agent to catch this at write time, zero LLM cost.**

### Implementation
- `internal/agent/prealloc_check.go` - detection logic (AST-based)
- `internal/agent/prealloc_check_test.go` - 14 tests
- Registered in `write_integrity.go` as `missing-prealloc` check for `LangGo`
