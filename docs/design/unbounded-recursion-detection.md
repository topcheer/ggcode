# Unbounded Recursion Detection

## Problem

AI coding agents frequently write recursive functions but forget the base case
(termination condition). The classic example:

```go
func factorial(n int) int {
    return n * factorial(n-1) // missing: if n <= 1 { return 1 }
}
```

This causes a stack overflow panic at runtime with no compile-time warning. Go's
compiler does not detect unbounded recursion. There is no widely-used Go linter
that catches missing base cases.

## Competitor Analysis

- **Claude Code**: no inline detection (relies on agent self-judgment)
- **Cursor**: lint-on-save doesn't catch missing base cases
- **Cline/OpenHands**: reactive only -- caught by tests or production crashes
- **Aider**: no detection
- **Devin**: no inline detection

## Solution

Post-write AST-based analysis added to the `checkWriteIntegrity` pipeline as
check #25. For each function that calls itself directly, we check whether every
execution path through the function body includes a self-call. If so, the
function has no termination path and will always overflow the stack.

### Path Analysis Model

Three path categories:

| Category | Meaning |
|---|---|
| `pathAlwaysRecurses` | Every path through this code includes a self-call |
| `pathEscapes` | At least one path exits via return without a self-call |
| `pathFallsThrough` | At least one path reaches the end without a self-call |

Key design decisions:

- **if/else**: if either branch escapes or falls through, the combined path is
  non-recursive (we can take that branch)
- **if without else**: the implicit else path (skipping the if) falls through
- **switch with no default**: can be skipped entirely if no case matches
- **for/range**: can be skipped if condition is initially false
- **defer f()**: does not execute immediately, falls through
- **go f()**: launches goroutine, falls through

### Delta-aware

Only flags NEW unbounded recursion introduced by the current edit. Pre-existing
patterns are not flagged.

### False positive avoidance

- Skips test files (intentional deep recursion in test helpers)
- Conservative: anything not understood defaults to `pathFallsThrough`
- Targets only functions where EVERY path recurses (near-zero false positives)
