# Variable Shadowing Detection

## Overview

Check #29 in the post-write integrity pipeline. Detects Go variable
shadowing where a `:=` declaration in an inner scope creates a new variable
that hides an outer-scope variable of the same name.

## Problem

Variable shadowing is one of the most common and dangerous Go bugs in
AI-agent-generated code. The classic pitfall with error variables:

```go
func process() error {
    err := db.Save(record)
    if err != nil {
        return err
    }
    if cond {
        err := validate(record) // SHADOWS outer err!
        // This err is a NEW variable. Outer err stays nil.
        // Even if validate returns an error, the outer err is unaffected.
    }
    return nil // outer err is nil even if validate failed
}
```

Error variable shadowing is particularly dangerous because:
1. The inner `err` is a NEW variable, not a reassignment
2. Errors are silently swallowed when the inner scope exits
3. `go vet` does not flag this pattern
4. `staticcheck` only flags loop variable shadowing (pre-Go 1.22)

## Detection

The check uses Go AST analysis to track variable scopes. For each function:

1. Collects names visible at the function's top-level scope (params, receiver,
   top-level `:=`/`var`/`const` declarations).
2. Walks nested scope-creating statements (`if`/`for`/`range`/`switch`/
   `type-switch`/`select`/closure bodies).
3. At each `:=` assignment in a nested scope, checks if the declared name
   already exists in the enclosing scope.

Error variables (`err`, `errs`, `e`) receive a more urgent warning since
shadowing them silently drops errors.

## Scope Coverage

The check detects shadowing in all Go scope-creating constructs:

- `if` / `else if` / `else` blocks
- `for` loops (including `for k, v := range`)
- `switch` case clauses
- `type-switch` case clauses
- `select` comm clauses (e.g., `case x := <-ch`)
- Function literals / closures
- Nested combinations of the above

## Competitor Analysis

| Tool | Inline Detection |
|------|-----------------|
| Claude Code | No |
| Cursor | Via gopls lint-on-save (not inline post-edit) |
| Cline/OpenHands | No (reactive only) |
| Aider | No |
| GitHub Copilot | No |

## Delta-Aware

Only flags shadowing that is NEW (introduced by the current edit). If the
same shadowing pattern existed before the edit, no warning is emitted.

## Integration

Registered as check #29 in `checkWriteIntegrity()` in `write_integrity.go`.
The check is AST-based, non-blocking, and runs synchronously after each
successful file write/edit.

Files:
- `internal/agent/shadow_check.go` - implementation
- `internal/agent/shadow_check_test.go` - 12 test cases covering all scope types
