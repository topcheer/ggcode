# Error Sentinel Comparison Detection (sa-63)

## Trend: Agentic Reliability / Error Handling Correctness

One of the most critical trends in AI Agent reliability is **correct error handling semantics**. AI-generated Go code frequently uses direct equality comparisons against sentinel errors (`err == sql.ErrNoRows`), which silently breaks when errors are wrapped using Go 1.13+ `%w` verb. The correct approach is `errors.Is(err, sentinel)`.

## Problem

Since Go 1.13, errors can be wrapped with `fmt.Errorf("...: %w", err)`, creating an error chain. Direct `==` comparison only checks the outermost error:

```go
// BUG: silently fails when err is wrapped
if err == sql.ErrNoRows { ... }

// CORRECT: traverses the entire error chain
if errors.Is(err, sql.ErrNoRows) { ... }
```

This is one of the most common sources of "works in dev, fails in prod" bugs because wrapping is often added later as code matures.

## Competitor Analysis

| Product | Write-time detection |
|---|---|
| Claude Code | No |
| Cursor | No (staticcheck SA1029 may catch some, but requires installed linters) |
| Cline/OpenHands | No |
| Aider | No |
| Windsurf | No |
| Devin | No |
| **ggcode** | **Yes (this check)** |

`go vet` does NOT flag this pattern. staticcheck's SA1029 only fires for specific known sentinel comparisons and is not write-time.

## Detection Approach

Pure AST-based analysis, zero LLM cost:

1. Scan for `BinaryExpr` nodes with `==` or `!=` operators
2. Check if one operand is an error-like variable (`err`, `e`, or names ending in `err`)
3. Check if the other operand is a sentinel error reference:
   - `*ast.SelectorExpr` where selector name starts with `Err` (e.g., `sql.ErrNoRows`)
   - `*ast.Ident` starting with `Err` (e.g., `ErrNotFound`)
   - Well-known stdlib sentinels (`io.EOF`, `context.Canceled`, `context.DeadlineExceeded`)
4. Exclude nil comparisons (handled by other checks)
5. Delta-aware: only flags patterns newly introduced by this edit

## Design Decisions

- **Error variable detection**: matches `err`, `e`, and names ending in `err` (e.g., `dbErr`, `parseErr`) to balance precision and recall
- **Sentinel name detection**: uses `Err` prefix convention plus an explicit allowlist for stdlib sentinels that don't follow the convention (`EOF`, `Canceled`, `DeadlineExceeded`)
- **Max 3 warnings per write**: prevents flooding the agent's context
- **Operator filtering**: only `==` and `!=` trigger; `<`, `>`, `<=`, `>=` are excluded
- **Non-Go files skipped**: language filter `LangGo` applied at registration

## Files

- `internal/agent/error_sentinel_check.go` (203 lines)
- `internal/agent/error_sentinel_check_test.go` (14 tests, all passing)
- `internal/agent/write_integrity.go` (1 line: registration)
