# Unchecked Type Assertion Detection (Post-Write Check)

## Problem

AI coding agents frequently produce Go code with unchecked type assertions:
`val := x.(SomeType)` without the comma-ok idiom. If the assertion fails at
runtime, Go panics with "interface conversion" - a crash that is often not
caught by tests (the failing input may be rare in the happy path) and
manifests as a production incident.

The safe pattern is the comma-ok form: `val, ok := x.(SomeType)` which
returns a zero value and `false` instead of panicking, allowing graceful
error handling.

## Competitor Analysis

- **Claude Code**: no automatic detection (relies on external linters)
- **Cursor**: no automatic detection (lint-on-save may catch via go vet)
- **Cline/OpenHands**: reactive only - caught by tests or production incidents
- **Aider**: no automatic detection
- **Windsurf**: no automatic detection

External linters (errcheck, staticcheck S1033) can catch some cases but
require a separate lint cycle and are not always installed. `go vet` does not
flag type assertions at all. This check provides immediate, zero-dependency
feedback at write time using Go's standard library AST parser.

## Approach

AST-based analysis. Walk all TypeAssertExpr nodes in the file. An assertion
is "unchecked" when it appears in a single-value context:

1. **Assignment with 1 LHS**: `s := v.(string)` - panics if v is not a string
2. **Call arguments**: `fmt.Println(v.(int))` - panics if v is not an int
3. **Return statements**: `return v.(int)` - panics, propagating to caller

The comma-ok form (`val, ok := v.(T)`) produces an AssignStmt with two LHS
operands and is excluded. Type switch guards (`switch t := v.(type)`) produce
a TypeAssertExpr with nil Type and are also excluded.

Detection is **delta-aware**: only NEW unchecked assertions introduced by
this edit are flagged, avoiding noise on pre-existing code.

## Detection Examples

Flagged (unsafe):
```go
s := v.(string)              // panics if v is not string
fmt.Println(v.(int))         // panics if v is not int
return v.(string)            // panics, propagates to caller
```

Safe (not flagged):
```go
s, ok := v.(string)          // comma-ok: ok=false instead of panic
s, ok = v.(string)           // assignment with comma-ok
switch t := v.(type) { ... } // type switch guard
```

## Integration

This check is integrated into the post-write integrity pipeline
(`checkWriteIntegrity` in `write_integrity.go`) as check #20. It runs
synchronously after file writes and injects warnings into the tool result so
the agent can fix the issue in the same turn.
