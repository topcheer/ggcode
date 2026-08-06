# Value Receiver Mutation Detection

## Problem

In Go, methods with **value receivers** operate on a COPY of the receiver.
Any mutations to receiver fields are silently lost when the method returns.
This is a subtle but dangerous bug that the Go compiler accepts without
warning.

```go
// BUG: mutations on a value receiver are lost
func (c Counter) Increment() {
    c.count++        // modifies a copy -- caller sees no change
    c.lastUpdate = time.Now()
}
```

The correct version uses a **pointer receiver**:

```go
func (c *Counter) Increment() {
    c.count++
    c.lastUpdate = time.Now()
}
```

## Why This Matters for AI Agents

LLM-generated Go code frequently mixes pointer and value receivers within
the same type. When the model generates a value-receiver method that
mutates state, the bug is invisible at compile time and only surfaces as
missing state updates at runtime.

## Competitor Analysis

| Tool            | Detection                                     |
|-----------------|-----------------------------------------------|
| Claude Code     | None                                          |
| Cursor          | Relies on external linters (inconsistent)     |
| Cline/OpenHands | None                                          |
| Aider           | None                                          |
| go vet          | Does not detect this pattern                  |
| staticcheck     | ST1016 warns about mixed receivers, not mutations |

No AI coding agent detects this at **write time**.

## Implementation

**File**: `internal/agent/value_recv_mutation_check.go`

**Approach**: AST-based analysis. For each `FuncDecl` with a value receiver:
1. Extract the receiver variable name (skip `_` and anonymous)
2. Walk the body for assignment and inc/dec statements targeting
   `receiverName.field`
3. Flag each mutation with line number and field name

**Detection scope**:
- `c.field = value` (assignment to receiver field)
- `c.field++` / `c.field--` (inc/dec on receiver field)
- `c.field += n` (compound assignment)
- Does NOT descend into nested function literals (those have closure scope)
- Skips pointer receivers (mutations there are correct)

**Complexity**: All functions under 15 cyclomatic complexity.

**Limits**: Max 4 warnings per file with truncation notice.

## Test Coverage

17 tests covering: direct assignment, increment, decrement, compound
assignment, pointer receiver (no warning), non-mutating method (no
warning), func literal (no warning), anonymous receiver, underscore
receiver, non-receiver variable, empty file, non-Go file, cap/truncation,
type name extraction.
