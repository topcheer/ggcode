# Unkeyed Struct Initialization Detection (Check #67)

## Trend Direction: Code Quality / API Design

AI coding agents frequently produce Go composite literals that initialize
structs positionally (unkeyed) rather than by field name. This is a well-known
anti-pattern that `go vet -composites` only catches for whitelisted stdlib
packages.

## Problem

```go
type Config struct {
    Host string
    Port int
    TLS  bool
}

// Bad -- unkeyed: fragile, error-prone
c := Config{"localhost", 8080, false}

// Good -- keyed
c := Config{Host: "localhost", Port: 8080, TLS: false}
```

Unkeyed initialization is dangerous because:
1. If struct fields are reordered, added, or removed, the code silently
   compiles but produces wrong values -- one of the hardest bugs to catch.
2. It's unreadable: the reader cannot tell which value maps to which field.
3. It violates Go idioms -- the Go style guide explicitly recommends keyed
   struct initialization.

## Competitor Analysis

| Tool | Detection | Scope |
|------|-----------|-------|
| go vet -composites | Yes | Only whitelisted stdlib packages |
| staticcheck | No | N/A |
| golangci-lint | Delegates to go vet | Same limitation |
| gocritic | No | N/A |
| Claude Code | No | N/A |
| Cursor | No | N/A |
| Aider | No | N/A |
| OpenHands/Cline | No | N/A |

**Gap**: No tool detects unkeyed struct initialization for user-defined types
at write time. ggcode is the first.

## Implementation

### Approach
AST-based analysis, zero LLM cost. Walks the parsed AST for `*ast.CompositeLit`
nodes, checks if the type resolves to a struct type, and flags when elements
are positional (no `*ast.KeyValueExpr`).

### Detection logic
1. Parse the Go source into an AST
2. Collect all locally-declared struct types with their field counts
3. Walk all `*ast.CompositeLit` nodes
4. For each composite:
   - Resolve the type name (handles `Ident` and `SelectorExpr`)
   - If the type is locally declared as a struct, confirm via field map
   - For qualified types (e.g., `http.Header`), flag with hedged wording if
     all elements are unkeyed; the kind cannot be confirmed without full
     type resolution, so no keyed-form suggestion is given (it would be a
     compile error for array/slice/map kinds)
   - Skip structs with <=1 field (trivially correct)
5. Delta-aware: filter out issues that existed in old content (per-key
   multiset count difference, so same-type same-count copy-paste additions
   are still caught)

### Files
- `internal/agent/unkeyed_struct_check.go` -- check implementation (230 lines)
- `internal/agent/unkeyed_struct_check_test.go` -- 14 tests
- `internal/agent/write_integrity.go` -- registration (1 line)

### Key design decisions
- **Delta-aware**: Uses `typeName:fieldCount` as dedup key (not line number)
  so line shifts from unrelated edits don't break delta filtering. Filtering
  is a per-key multiset count difference (not set membership), so adding a
  second same-type same-element-count literal is still reported (#1215)
- **Field count handling**: Uses `structFieldCount()` helper that correctly
  counts multi-name fields (`A, B, C int` = 3 fields, not 1)
- **Qualified types**: For cross-package types (e.g., `http.Header{...}`),
  flags with hedged wording if all elements are positional. The warning
  never suggests a keyed literal form because the qualified name may be an
  array/slice/map/basic alias, for which such a suggestion would be a
  compile error (#1216). Full `go/types` resolution remains the root fix
  (deferred as too costly for write-time advisory)
- **Warning cap**: Max 4 warnings + truncation notice
- **Test file skip**: Test files use positional init commonly for mocks

### Max cyclomatic complexity
All functions are under 15.

## Test Coverage
18 tests covering:
- Basic unkeyed detection
- Keyed init is OK
- Delta-awareness (old content filtering)
- Slice/map init excluded
- Qualified type detection (hedged wording, no keyed-form suggestion)
- Confirmed struct keeps strong wording
- Same-type same-count literal addition flagged (multiset delta)
- Line-shift no-reflag
- Small struct (1 field) skip
- Empty content
- Non-Go file skip
- Test file skip
- Multiple issues
- Warning cap
- All-elements-unkeyed edge cases
