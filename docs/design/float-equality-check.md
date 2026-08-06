# Float Equality Comparison Detection (Check #76)

## Problem

Comparing floating-point values with `==` or `!=` is a classic source of subtle bugs.
Due to IEEE 754 representation, computations like `0.1 + 0.2` produce `0.30000000000000004`,
not `0.3`. AI coding agents frequently produce code that uses exact equality for floats,
leading to intermittent failures that are hard to debug.

## Correct Pattern

```go
// Bad: exact float comparison
if result == 0.3 { ... }

// Good: epsilon tolerance comparison
if math.Abs(result - 0.3) < 1e-9 { ... }
```

## Implementation

**File**: `internal/agent/float_equality_check.go`
**Registration**: `write_integrity.go` as `float-equality` check
**Language**: Go only

### Detection Strategy (AST-based, zero LLM cost)

1. Parse Go file into AST
2. Collect all float-typed variables (float32/float64) from:
   - Package-level `var` declarations
   - Local `var` declarations inside function bodies
3. Walk all binary expressions with `==` or `!=` operators
4. Check if either operand is:
   - A float literal (e.g., `0.1`, `3.14`)
   - A float-typed variable (from collected set)
   - A `math.*` function call (returns float64)
   - Float arithmetic (recursive: binary expr with float operand)
   - Parenthesized float expression
5. Warn with line number and operator

### Limits

- Max 5 warnings per file (+ truncation notice)
- Skips non-Go files and unparseable code silently

## Competitor Analysis

| Tool | Write-time detection | Notes |
|------|---------------------|-------|
| Claude Code | No | |
| Cursor | No | staticcheck SA9000 may catch post-save |
| OpenHands/Cline | No | |
| Aider | No | |
| golangci-lint | No (by default) | |
| **ggcode** | **Yes** | |

## Test Coverage

16 tests covering: basic literals, != operator, declared vars, int (no warning),
math functions, float arithmetic, < operator (no warning), non-Go files, empty content,
package-level vars, max warnings cap, float32, parenthesized expressions, string comparison,
invalid Go code.
