# Error Swallowing Detection

## Problem

AI coding agents (including Claude Code, Cursor, Cline, Aider) frequently produce
Go code with incomplete error handling. The two most common failure modes:

1. **Empty error check**: `if err != nil { }` - the agent writes the error check
   skeleton but forgets to fill in the body. The error is silently swallowed.

2. **Bare return on error**: `if err != nil { return }` - in a function that
   returns `error`, a bare `return` sends nil/zero values, hiding the real error
   from callers.

Both patterns produce code that compiles but has incorrect runtime behavior.

## Competitor Analysis

| Tool | Detection Method | Timing |
|------|-----------------|--------|
| Claude Code | External linters (errcheck) | Post-hoc |
| Cursor | Lint-on-save (golangci-lint) | IDE-bound |
| Cline/OpenHands | Test/build failures | Reactive |
| Aider | None | N/A |
| **ggcode** | **AST-based inline check** | **Write-time** |

## Implementation

**File**: `internal/agent/error_swallow_check.go`

The check runs as part of the post-write integrity pipeline (check #18),
alongside resource leak detection, syntax validation, and placeholder detection.

### Detection Logic

For each `if errVar != nil` block in a Go function:

1. **Empty body detection**: If the block has zero statements, flag as
   "Empty error handler". Comments alone don't count as statements in the AST.

2. **Bare return detection**: If the block contains a `return` with no values
   AND the enclosing function declares an `error` return type, flag as
   "Bare return swallows error".

### Delta-Aware Design

Only **newly introduced** patterns are flagged. The check counts instances in
the old content vs new content and only reports the delta. This prevents noise
on files with pre-existing error handling issues.

### Error Variable Matching

Recognizes standard Go error variable naming conventions:
- `err`, `e`, `errs`
- `err1`, `err2` (numbered suffixes for multiple calls)
- `parseErr`, `dbError` (suffix-based naming)

### Performance

Uses Go's standard library `go/parser` (pure in-memory, <1ms per typical file).
Shares the AST parse with other checks when possible.

## False Positive Mitigations

- **Void functions**: Bare `return` in functions that don't return error is
  valid and not flagged.
- **Proper handling**: `return err`, `return fmt.Errorf(...)`, `log.Fatal(err)`
  etc. all produce non-empty bodies and are not flagged.
- **Named returns**: Bare `return` in named-return functions does propagate
  errors correctly, but the suggestion to use explicit `return err` is still
  good practice and the warning is intentionally conservative here.
