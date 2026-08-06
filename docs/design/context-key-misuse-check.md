# Context Value Key Type Misuse Detection (sa-68)

## Trend / Direction

**Category**: Go Code Quality / Context Propagation

Go's `context.WithValue` documentation explicitly warns against using built-in
types (especially strings) as keys:

> "The provided key must be comparable and should not be of type string or any
> other built-in type to avoid collisions between packages using context."

AI coding agents frequently write `context.WithValue(ctx, "userID", 42)` as a
quick solution, introducing silent cross-package key collisions.

## Competitor Analysis

| Tool | Write-time detection | Notes |
|------|---------------------|-------|
| Claude Code | No | Relies on agent judgment |
| Cursor | No | golangci-lint has no rule for key types |
| Cline/OpenHands | No | Reactive only |
| Aider | No | No detection |
| GitHub Copilot | No | May suggest good patterns but doesn't verify |
| go vet | No | Only checks cancel propagation |
| staticcheck | No | No rule for context key types |
| gosec | No | G104 covers unchecked errors only |

**None of the major tools detect this at write time.**

## Gap in ggcode

The existing `context_leak_check.go` detects `context.TODO()`/`context.Background()`
usage inside functions that receive a `ctx context.Context` parameter. However,
it does NOT detect `context.WithValue` calls with string/numeric literal keys.

This is a separate class of bug:
- Context leak = lost cancellation/deadline propagation
- Key misuse = silent value collision across packages, broken type safety

## Implementation

**File**: `internal/agent/ctxkey_check.go`

### Detection approach

AST-based analysis of Go source. Detects `context.WithValue` calls where the
key argument (second parameter) is a `*ast.BasicLit` with kind STRING, INT, or
FLOAT.

### Patterns detected

1. `context.WithValue(ctx, "userID", 42)` -- string literal key
2. `context.WithValue(ctx, 1, "value")` -- int literal key
3. `context.WithValue(ctx, 3.14, "pi")` -- float literal key

### Patterns NOT flagged (correct usage)

- `context.WithValue(ctx, userIDKey, 42)` where `userIDKey` is a custom type constant
- `context.WithValue(ctx, myKey, val)` where `myKey` is a variable reference
- Aliased context packages (`ctx2.WithValue(...)`) -- only literal `"context"` is checked

### Properties

- **Delta-aware**: Only flags NEW occurrences introduced by this edit
- **Test files skipped**: Avoids false positives in test assertions
- **Zero LLM cost**: Pure AST pattern matching
- **No external dependencies**: Uses only Go standard library
- **Max warnings**: 4 per write (with truncation notice)
- **Complexity**: All functions under cyclomatic complexity 15

## Test Coverage

12 tests in `ctxkey_check_test.go`:
- String key detection
- Int key detection
- Float key detection
- Custom type key (pass)
- Delta-aware (only new keys flagged)
- Test files skipped
- Non-Go files skipped
- Multiple warnings cap
- No WithValue (pass)
- Empty content (pass)
- Const reference key (pass)
- Aliased context package (pass)

## Registration

Registered in `write_integrity.go` `allChecks` as:
```go
{Name: "context-key-misuse", Langs: []Language{LangGo}, Run: sliceCheck(checkContextKeyMisuse)},
```
