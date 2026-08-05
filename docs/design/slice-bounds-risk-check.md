# Slice Bounds Risk Detection

## Problem

AI coding agents frequently index into slices returned by functions that may
produce empty or nil results without checking the length first. This causes
runtime panics (`index out of range`), which are among the most common crashes
in production Go services.

**Trend**: Input Validation & Runtime Safety -- a core concern in the AI Agent
space where generated code must be correct on the first try. No major AI coding
tool (Claude Code, Cursor, Cline/OpenHands, Aider, Windsurf, Devin) detects
this pattern at write time.

## Patterns Detected

1. **regexp.FindStringSubmatch** -> `match[1]` without nil/len check
   - Returns `nil` when no match is found; indexing `nil` panics
2. **strings.Split** -> `parts[1]` without len check
   - Returns >=1 element, but `parts[1]` panics if separator is absent
3. **strings.Fields** -> `fields[0]` without len check
   - Returns empty slice when input is all whitespace
4. **bytes.Split / bytes.Fields / filepath.SplitList** -> same patterns
5. **regexp.FindAll\*** methods -> same patterns

## Risky Function Registry

| Function | Min Risky Index | Rationale |
|----------|----------------|-----------|
| `strings.Split` | 1 | [0] always exists; [1] panics if separator absent |
| `strings.SplitN` | 1 | Same as Split |
| `strings.Fields` | 0 | Returns empty slice on whitespace-only input |
| `strings.FieldsFunc` | 0 | Same as Fields |
| `bytes.Split` | 1 | Same as strings.Split |
| `bytes.Fields` | 0 | Same as strings.Fields |
| `filepath.SplitList` | 1 | [0] always exists |
| `regexp.FindStringSubmatch` | 0 | Returns nil on no match |
| `regexp.FindSubmatch` | 0 | Returns nil on no match |
| `regexp.FindAllStringSubmatch` | 0 | Returns nil on no match |
| `regexp.FindAllString` | 0 | Returns nil on no match |
| `regexp.FindAllSubmatch` | 0 | Returns nil on no match |

## Approach

AST-based analysis with function-body scope tracking:

1. For each function, walk the AST tracking assignments from known risky
   slice-returning functions (e.g., `parts := strings.Split(s, ",")`)
2. When an `IndexExpr` is found on a tracked variable, check if the literal
   index >= the function's minimum risky index
3. Check if a `len(varName)` guard exists between the assignment and the index
   access (text-based scan of source lines)
4. Flag if no guard is found

Delta-aware: only flags patterns INTRODUCED by the current edit.

## Design Decisions

- **Scope**: tracks variables within a single function body. Cross-function
  tracking is not attempted (would require whole-program analysis).
- **Guard detection**: uses simple `len(varName)` substring match in source
  lines between assignment and index access. This catches `if len(x) > N`,
  `if len(x) == N`, and `switch len(x)` patterns without AST complexity.
- **Reassignment**: if a tracked variable is reassigned from a non-risky
  function, tracking is removed (avoids false positives).
- **Non-literal indices**: skipped (e.g., `parts[i]` -- we can't statically
  determine the risk without data flow analysis).
- **Zero LLM cost**: pure AST + text matching, no model calls.

## Competitor Analysis

| Tool | Detection | Mechanism |
|------|-----------|-----------|
| Claude Code | None | - |
| Cursor | None at write time | Runtime panic only |
| Cline/OpenHands | None | - |
| Aider | None | - |
| Windsurf | None | - |
| Devin | None | - |
| go vet | None | Does not check slice indexing |
| staticcheck | None | No rule for this pattern |
| golangci-lint | None | No built-in linter |

**ggcode is the only tool that catches this at write time.**

## Files

- `slice_bounds_check.go` -- check implementation (281 lines)
- `slice_bounds_check_test.go` -- 15 test cases
- 1 line in `write_integrity.go` for registration
