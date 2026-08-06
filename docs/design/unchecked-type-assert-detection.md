# Unchecked Type Assertion Detection

## Problem

Go's type assertion `x.(T)` panics at runtime if the interface doesn't hold type T. The safe comma-ok form `val, ok := x.(T)` returns a zero value and `false` instead. AI coding agents frequently produce unchecked assertions that pass happy-path tests but crash in production with rare interface values.

No major AI coding agent (Claude Code, Cursor, OpenHands, Devin, Aider, Windsurf) detects this at write time. go vet doesn't flag type assertions. staticcheck S1033 catches some cases but requires a separate lint cycle.

## Implementation

**Files**: `internal/agent/unchecked_assert_check.go`, `unchecked_assert_check_test.go`

**Approach**: Pure AST analysis using Go stdlib `go/parser`. Walks all `*ast.TypeAssertExpr` nodes, excludes:
- Comma-ok form (2 LHS in assignment or 2 names in ValueSpec)
- Type switch guards (`switch v := x.(type)` — nil Type field)

**Delta-aware**: Only warns when new assertions are introduced (new count > old count).

**Registration**: `write_integrity.go` line 135 — `unchecked-type-assert` check for `LangGo`.

## Tests

10 tests covering: unchecked assignment, comma-ok safety, call argument context, delta-awareness, multiple new assertions, non-Go files, empty content, var-decl form, assignment comma-ok, type switch safety.
