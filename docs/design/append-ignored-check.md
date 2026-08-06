# Append Ignored Check

## Problem

In Go, `append()` returns a new slice. If the return value is not assigned,
the original slice is unchanged and the append is silently lost:

```go
var items []int
append(items, 42)  // BUG: items is still empty
```

The Go compiler does not warn about this. staticcheck (SA4017) detects it
at lint time, but no AI coding agent catches it at write time.

## Detection

AST-based analysis. Flags any standalone `ExprStmt` whose expression is a
direct call to the builtin `append`. Only the clearest case (bare standalone
statement) is flagged to minimize false positives.

### False positive avoidance

- `_ = append(s, x)` -- assignment, not flagged
- `foo(append(s, x))` -- result consumed by function call, not flagged
- `obj.append(x)` -- method call, not builtin, not flagged
- `s = append(s, x)` -- proper assignment, not flagged

## Files

- `append_ignored_check.go` -- implementation (~70 LOC)
- `append_ignored_check_test.go` -- 11 test cases
- `write_integrity.go` -- 1 registration line

## Complexity

All functions under cyclomatic complexity 5. Zero LLM cost. No external
dependencies.
