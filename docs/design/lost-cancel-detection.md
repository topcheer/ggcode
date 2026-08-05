# Lost Cancel Detection (Post-Write Check)

## Problem

AI coding agents (and human developers) frequently produce Go code that calls
`context.WithCancel(ctx)`, `context.WithTimeout(ctx, d)`, or
`context.WithDeadline(ctx, t)` but forgets to call the returned cancel function
(or defer it). The cancel function MUST be called to release resources
associated with the derived context:

- **Timer leaks**: `WithTimeout`/`WithDeadline` create internal timers that are not
  garbage collected until they fire.
- **Goroutine leaks**: The `context` package spawns a goroutine per derived context
  to propagate cancellation to children.
- **Memory growth**: In long-running services, uncanceled contexts accumulate on the
  heap, causing gradual memory pressure and eventual OOM.

The Go standard library documentation explicitly states:

> Failing to call the Cancel function leaks the child and its subtree until the
> parent is canceled or the timer fires.

## Competitor Analysis

- **go vet (lostcancel)**: Detects this via a separate `go vet` cycle. Requires
  explicit invocation and is not always run.
- **staticcheck (SA9003)**: Flags the broader "empty branch" but not
  specifically lostcancel.
- **Claude Code**: No inline detection at write time.
- **Cursor**: May catch via lint-on-save if golangci-lint is configured.
- **Cline/OpenHands**: Reactive only, caught by tests or production incidents.
- **Aider**: No automatic detection.
- **Windsurf**: No automatic detection.
- **GitHub Copilot**: Sometimes warns via lint integration.
- **Devin**: No inline detection.

**Gap**: No AI coding agent provides inline lostcancel detection at write time.
External linters require a separate cycle and are not always installed. This check
provides immediate, zero-dependency, zero-LLM-cost feedback in <1ms per file.

## Approach

AST-based analysis using Go's standard library `go/ast` parser. For each function:

1. Find assignments where the RHS is `context.WithCancel`/`WithTimeout`/`WithDeadline`.
2. Extract the cancel variable name (second return value, LHS index 1).
3. Search the function body for any call to that variable (deferred or direct).
4. If not found, emit a warning guiding the user to add `defer cancel()`.

### Patterns Detected

```go
// DETECTED: cancel never called
ctx, cancel := context.WithCancel(parent)
_ = ctx

// DETECTED: cancel never called (timer leak)
ctx, cancel := context.WithTimeout(parent, 5*time.Second)
_ = ctx

// DETECTED: cancel assigned to blank identifier with timer
ctx, _ := context.WithTimeout(parent, 5*time.Second)
// Cancel discarded, timer not released until fire

// CLEAN: defer cancel() present
ctx, cancel := context.WithCancel(parent)
defer cancel()

// CLEAN: cancel called directly
ctx, cancel := context.WithCancel(parent)
if err := work(ctx); err != nil {
    cancel()
    return err
}
cancel()

// CLEAN: blank-id WithCancel (no timer, acceptable)
ctx, _ := context.WithCancel(parent)
```

### Special Cases

- **Blank identifier with `WithCancel`**: No warning (no timer to leak).
- **Blank identifier with `WithTimeout`/`WithDeadline`**: Warning (timer not released).
- **Delta-aware**: Only flags NEW lostcancel occurrences introduced by this edit,
  not pre-existing issues.
- **Parse errors**: Silently returns no warnings (does not block writes).

## Files

- `internal/agent/defer_cancel_check.go` - Implementation (~230 lines)
- `internal/agent/defer_cancel_check_test.go` - 13 test cases
- `internal/agent/write_integrity.go` - Registration (1 line)

## Registration

```go
{Name: "lost-cancel", Langs: []Language{LangGo}, Run: sliceCheck(checkLostCancel)},
```

## Performance

- Zero LLM cost (deterministic AST pattern matching).
- <1ms per file using Go standard library parser.
- Runs in parallel with other post-write integrity checks via the check registry.
- Capped at 3 warnings per write to prevent context noise.
