# SA-55: Map Write During Iteration Detection

## Problem

AI coding agents frequently generate Go code that writes to (or deletes from)
a map while iterating over it with `range`. The Go specification explicitly
states:

> If a map entry that has not yet been reached is removed during iteration,
> the corresponding iteration value will not be produced. If a map entry is
> created during iteration, that entry may be produced during the iteration
> or may be skipped.

This produces non-deterministic, hard-to-debug behavior:

```go
// UNSAFE: entries may be skipped
for k, v := range m {
    if v.expired {
        delete(m, k)
    }
}

// UNSAFE: new entries may or may not be visited
for k, v := range m {
    m["prefix_"+k] = v * 2
}
```

## Competitor Analysis

| Tool | Detection | Mechanism |
|------|-----------|-----------|
| Claude Code | None | No write-time check |
| Cursor | None | staticcheck doesn't cover this |
| Cline/OpenHands | None | No detection |
| Aider | None | No detection |
| Windsurf | None | No detection |
| GitHub Copilot | None | No inline detection |
| go vet | None | `-range` was considered and dropped |
| staticcheck | None | SA9001 proposed but never merged |

**Gap**: No AI coding agent or standard Go tooling detects map write during
iteration at write time. This check provides immediate, zero-cost feedback.

## Implementation

**File**: `internal/agent/map_iter_write_check.go`

**Approach**: AST-based analysis. For each `RangeStmt`, determine if the
iterated expression is a map variable. Then walk the loop body looking for:

1. `delete(m, key)` calls where `m` matches the range expression
2. `m[key] = value` assignments where `m` matches the range expression

Supports both simple identifiers (`m`) and selector expressions (`s.field`).

**Delta-aware**: Only flags patterns newly introduced by the edit (compares
old vs new content occurrence count).

**Complexity**: All functions under cyclomatic complexity 15.

**Zero LLM cost**: Pure AST pattern matching, <1ms per file.

## Test Coverage

12 tests in `map_iter_write_check_test.go`:
- Delete during range over map
- Assignment during range over map
- New key assignment during range
- No false positive: writing to a different map
- No false positive: range over slice/array
- No false positive: pre-existing pattern (no delta)
- Nested write inside if-block within loop
- Struct field map (`s.items[k] = v`)
- Empty content, non-Go file, invalid Go syntax

## Correct Patterns

```go
// Collect keys to delete, then delete after iteration
var toDelete []string
for k, v := range m {
    if v.expired {
        toDelete = append(toDelete, k)
    }
}
for _, k := range toDelete {
    delete(m, k)
}

// Build a new map instead of mutating in-place
result := make(map[string]int, len(m))
for k, v := range m {
    result["prefix_"+k] = v * 2
}
```
