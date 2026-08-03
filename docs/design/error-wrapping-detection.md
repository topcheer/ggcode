# Inconsistent Error Wrapping Detection

## Problem

AI coding agents frequently produce Go code with broken error wrapping. Go 1.13
introduced the `%w` verb for error wrapping in `fmt.Errorf`, but agents often:

1. **`%v` instead of `%w`**: `fmt.Errorf("failed: %v", err)` - compiles fine, but
   callers using `errors.Is()` or `errors.As()` cannot unwrap the causal error.
   The error chain is silently lost.

2. **`errors.New(err.Error())`**: Creates a brand-new error from the string
   representation, losing the original error type and wrapping chain entirely.

3. **String concatenation**: `fmt.Errorf("failed: " + err.Error())` - same chain
   breakage, plus format verbs in the error message can corrupt output.

These are subtle bugs: code compiles, tests may pass, and error messages look
correct. But any downstream code using `errors.Is(err, os.ErrNotExist)` or
`errors.As(err, &pathErr)` will silently fail.

## Competitor Analysis

| Tool | Detection Method | Timing |
|------|-----------------|--------|
| Claude Code | None (relies on agent judgment) | N/A |
| Cursor | staticcheck S1028 (partial) | IDE-bound |
| Cline/OpenHands | None | N/A |
| Aider | None | N/A |
| GitHub Copilot | May suggest `%w` but doesn't verify | Suggestion-only |
| **ggcode** | **AST-based inline check** | **Write-time** |

`go vet` does NOT flag `%v` vs `%w` in Errorf - both are valid format verbs.
`staticcheck`'s S1028 catches `errors.New(fmt.Sprintf(...))` but not the subtler
patterns detected here.

## Implementation

**File**: `internal/agent/error_wrap_check.go`

The check runs as part of the post-write integrity pipeline (check #33),
alongside error swallowing detection and ignored error return detection.

### Detection Logic

For each `fmt.Errorf` and `errors.New` call in a Go file:

1. **`%v` with error argument**: If a constant format string contains `%v` and
   the corresponding argument looks like an error variable (named `err`, `e`,
   or ending in `err`/`error`), flag it. Skip if the format already contains
   `%w` (conservative: the `%v` might be for a non-error arg).

2. **`errors.New(err.Error())`**: If `errors.New` is called with an expression
   that contains a `.Error()` method call, flag it.

3. **String concatenation in Errorf**: If the first argument to `fmt.Errorf`
   is a `BinaryExpr (+)` containing `.Error()` anywhere in the chain, flag it.

### Key Design Decisions

- **Delta-aware**: Only flags patterns introduced by this edit (compares
  old vs. new occurrence counts).
- **Test files skipped**: Error wrapping in tests is less critical.
- **Non-blocking advisory**: Reports issues for the agent to fix, does not
  block the write.
- **Zero-LLM-cost**: Uses Go stdlib AST parser, no model calls.
- **Conservative heuristics**: `looksLikeErrorArg` only matches common error
  variable names to avoid false positives on non-error arguments.
- **Capped at 2 warnings per write** to avoid flooding tool results.

### Supported Patterns

```go
// FLAGGED: %v loses wrapping chain
return fmt.Errorf("failed: %v", err)

// CORRECT: %w preserves wrapping chain
return fmt.Errorf("failed: %w", err)

// FLAGGED: errors.New with err.Error() loses type info
return errors.New(err.Error())

// FLAGGED: string concat loses wrapping chain
return fmt.Errorf("failed: " + err.Error())

// NOT FLAGGED: %v with non-error argument
return fmt.Errorf("count: %v", count)
```
