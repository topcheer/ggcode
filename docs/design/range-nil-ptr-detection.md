# Range Over Nil Pointer Detection

## Problem

AI coding agents sometimes produce Go code that ranges over a pointer to a
slice or map without first checking if the pointer is nil:

```go
func process(items *[]Item) {
    for _, item := range *items {   // panics if items == nil
        handle(item)
    }
}
```

If the pointer is nil, the dereference `*items` causes a **nil pointer
dereference panic** at runtime -- one of the most common Go crashes.

## Detection

This check is purely AST-based (zero LLM cost). It inspects every `RangeStmt`
in the file and checks if the range expression (`rs.X`) is a `*ast.StarExpr`
(pointer dereference). If so, it warns that the pointer may be nil.

Two sub-patterns are detected:
1. **Simple variable**: `for _, v := range *ptr` -- warns with the variable name
2. **Complex expression**: `for _, v := range *getPtr()` -- warns that the
   result should be stored in a variable and nil-checked first

## Competitor Gap

No major AI coding agent or standard Go tooling detects this at write time:
- **Claude Code**: no write-time detection
- **Cursor**: no write-time detection  
- **go vet**: does not flag this pattern
- **staticcheck (SA5011)**: detects some nil dereferences but is conservative
  on range expressions

## Implementation

- **File**: `internal/agent/range_nil_ptr_check.go`
- **Tests**: `internal/agent/range_nil_ptr_check_test.go` (12 tests)
- **Registration**: 1 entry in `write_integrity.go`
- **Complexity**: all functions under cyclomatic complexity 15
- **Naming**: all helpers prefixed `rnp` to avoid collisions

## Limitations

The check is intentionally conservative -- it does not track nil guards across
control flow. This means it may warn even when a nil check exists earlier in
the function body. This is acceptable for an advisory write-time check: false
positives cost seconds to dismiss, while false negatives cost hours of
debugging runtime panics.
