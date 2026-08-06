# Constant Conditional Detection (if true / if false)

## Design Document

### Problem

AI coding agents sometimes emit `if` statements whose conditions are constant
at compile time, such as:

```go
if true { ... }      // else-branch is dead code
if false { ... }     // then-branch is dead code
if 1 == 1 { ... }    // constant comparison from copy-paste
if !true { ... }     // negated boolean literal
```

These are almost always bugs: dead code hiding logic that will never execute,
half-finished refactors with stubbed-out predicates, or templating mistakes.

### Detection

**Method**: Pure AST constant evaluation. Parse the file, walk all `IfStmt`
nodes, evaluate the condition expression to a compile-time boolean using
`go/constant` from the standard library.

**Supported constant forms**:
- Boolean literals: `true`, `false`
- Unary negation: `!true`, `!false`
- Logical operators: `&&`, `||` with constant operands
- Comparison operators: `==`, `!=`, `<`, `>`, `<=`, `>=` with constant
  boolean or numeric operands
- Parenthesized expressions and unary `+`/`-` on numeric literals

**Not flagged** (correctly): variable conditions, function calls, type
conversions, named constants from other packages.

### Design Decisions

1. **No descent into dead bodies**: When a constant-conditional is found, the
   visitor returns `false` to skip the body. Nested `if false` inside an
   `if true` block would only add noise.

2. **go/constant for numeric comparison**: Using `constant.Compare` handles
   integer, float, and comparison operator evaluation correctly without
   manual type checking.

3. **Boolean comparison before numeric**: For `==` and `!=`, boolean operands
   are checked first (since bool is not numeric in go/constant), then numeric
   operands.

4. **Helper prefix `cc`**: All internal helpers use the `cc` prefix
   (`ccBoolValue`, `ccBinaryBool`, `ccLogicalBool`, `ccCompareBool`,
   `ccConstValue`, `ccEmitWarning`) to avoid naming collisions with the 200+
   existing check files.

5. **Max 5 warnings**: Follows the project convention of capping warnings
   with a truncation message to avoid overwhelming the context.

### Complexity

All functions have cyclomatic complexity under 15:
- `checkConstantConditional`: ~6 (guard clauses, inspect callback, truncation)
- `ccBoolValue`: ~8 (type switch on expression kinds)
- `ccBinaryBool`: ~5
- `ccLogicalBool`: ~4
- `ccCompareBool`: ~6
- `ccConstValue`: ~7
- `ccEmitWarning`: ~2

### Competitor Analysis

| Tool | Detection |
|------|-----------|
| Claude Code / Cursor / OpenHands / Aider | No write-time detection |
| staticcheck (SA4023) | Flags impossible comparisons, not literal `if true`/`if false` uniformly |
| golangci-lint | Relies on staticcheck; no dedicated write-time constant-condition check |

### Files

- `internal/agent/constant_conditional_check.go` (181 lines)
- `internal/agent/constant_conditional_check_test.go` (16 tests)
- `internal/agent/write_integrity.go` (1 registration entry)

### Zero LLM Cost

Pure AST walking and constant evaluation. No external dependencies beyond
Go standard library (`go/ast`, `go/constant`, `go/parser`, `go/token`).
