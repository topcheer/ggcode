# Deferred Call Argument Evaluation Detection (Check #77)

## Problem

In Go, `defer f(args)` evaluates all arguments **immediately** at the defer
statement, not when the deferred function actually runs. This is one of the
most common subtle bugs in Go:

```go
defer log.Printf("took %v", time.Since(start))  // BUG: time.Since(start) evaluated NOW
defer fmt.Println(getMessage())                  // BUG: getMessage() called immediately
defer db.Exec(query, buildArgs(req))             // BUG: buildArgs(req) runs now
```

The developer typically intends these arguments to be evaluated at defer
time (i.e., when the surrounding function returns). The fix is to wrap in
a closure:

```go
defer func() { log.Printf("took %v", time.Since(start)) }()
```

### Competitor Gap Analysis

| Tool | Detection | Timing |
|------|-----------|--------|
| Claude Code | None | N/A |
| Cursor | None | N/A |
| OpenHands | None | N/A |
| Cline | None | N/A |
| Aider | None | N/A |
| golangci-lint | None | N/A |
| staticcheck | None | N/A |
| `go vet` | None | N/A |

No existing tool detects this pattern at write time.

## Implementation

**File**: `internal/agent/defer_arg_eval_check.go` (114 lines)
**Test**: `internal/agent/defer_arg_eval_check_test.go` (11 tests)
**Registration**: 1 entry in `write_integrity.go`

### Approach

AST-based analysis using `go/parser`:

1. Parse the file into a Go AST
2. Walk all function declarations
3. For each `*ast.DeferStmt`:
   - Skip if the deferred call is a `*ast.FuncLit` (closure -- safe)
   - For each argument, check if it contains a `*ast.CallExpr`
   - If found, emit a warning about eager evaluation

### Key Design Decisions

- **Prefix `dae`** for all helper functions to avoid naming collisions
- **Max 5 warnings** per file with truncation notice
- **Zero LLM cost** -- pure AST pattern matching
- **Go-only** -- registered with `Langs: []Language{LangGo}`
- **Excludes closures** -- `defer func(){}()` is the safe pattern, no warning

### Cyclomatic Complexity

`checkDeferArgEval`: ~9 (well under 15 limit)
`daeContainsCall`: ~2

### Test Coverage

11 tests covering:
- No args (safe)
- Closure defer (safe)
- Function call argument (flagged)
- log.Printf with call arg (flagged)
- Literal args (safe)
- Nested call arg (flagged)
- Non-Go file (skipped)
- Empty content (skipped)
- Variable arg (safe)
- Max warnings cap
- Method call arg (flagged)
