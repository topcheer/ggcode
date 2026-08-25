# Regex Compile-in-Loop Detection (Design Doc)

## Trend: AI Agent Performance Awareness - Expensive Operation in Loop

## Problem

AI coding agents frequently generate Go code that compiles regex patterns inside
for/range loops using `regexp.Compile`, `regexp.MustCompile`, `regexp.CompilePOSIX`,
or `regexp.MustCompilePOSIX`. Each call re-parses and compiles the pattern from
scratch - an O(m) operation where m is the pattern length - even when the pattern
string is identical across iterations.

```go
for _, input := range inputs {
    re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`) // compiled N times!
    if re.MatchString(input) { ... }
}
```

For N iterations, this wastes O(N*m) CPU and allocates N Regexp objects that are
immediately garbage-collected. Benchmark: compiling a typical regex takes ~5-20
microseconds; for N=10,000 iterations that's 50-200ms of wasted CPU.

## Correct Pattern

Compile once at package level and reuse:

```go
var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

for _, input := range inputs {
    if dateRe.MatchString(input) { ... }
}
```

## Competitor Analysis

| Competitor | Detects at Write Time? | Notes |
|---|---|---|
| Claude Code | No | Relies on external profilers |
| Cursor | No | Lint-on-save does not flag regex compile in loops |
| Cline/OpenHands | No | Reactive only - caught by profiling |
| Aider | No | No detection |
| Devin | No | No inline detection |
| go vet | No | Does not flag regex compile in loops |
| staticcheck | No | Does not flag regex compile in loops |
| gocritic | No | No equivalent check |

**Gap**: No major competitor or linter detects this at write time.

## Detection Approach

AST-based analysis:
1. Parse the Go source into an AST
2. Walk every `ForStmt` and `RangeStmt` node
3. Within each loop body, recursively inspect (through nested if/switch/select)
   for `CallExpr` nodes whose function name matches a regex compile function
4. Skip nested `FuncLit` nodes (separate scope, may be called via goroutine)
5. Delta-aware: only flag patterns newly introduced by the edit

### Functions Detected

- `regexp.Compile`
- `regexp.MustCompile`
- `regexp.CompilePOSIX`
- `regexp.MustCompilePOSIX`

## Distinction from Existing Checks

| Check | What it Detects | Overlap? |
|---|---|---|
| `loop-perf` (#36) | O(n^2) string concatenation in loops | No - different cost pattern |
| `nplus1-loop` (#48) | I/O operations (DB/HTTP/file) in loops | No - I/O vs CPU |
| **`regex-loop`** (this) | CPU-bound regex compilation in loops | New |

## Files

- `internal/agent/regex_loop_check.go` - check implementation (172 lines)
- `internal/agent/regex_loop_check_test.go` - 12 tests, all passing
- `internal/agent/write_integrity.go` - registered in `registerAllChecks()` ("regex-loop" entry; fc5c4aad silently stripped it and #1020 re-registered per the #508/#516 revival precedent)

## Design Decisions

1. **Delta-aware**: Compares old vs new content, only flags newly introduced patterns
2. **Capped at 2 individual warnings** + summary line for overflow, to avoid flooding
3. **Skips test files** is NOT applied - regex-in-loop is a real perf bug in any file
4. **Skips nested FuncLit** - regex compile inside a closure nested in a loop may be
   called once (e.g., `sync.Once`), so flagging would be a false positive
5. **Zero LLM cost** - pure AST pattern matching
6. **No external dependencies** - uses only stdlib `go/ast`, `go/parser`, `go/token`
7. **Cyclomatic complexity**: `checkRegexLoop` = 6, `findRegexInLoops` = 6 (both < 15)
