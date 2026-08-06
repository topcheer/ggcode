# Naked Return in Long Function Detection

## Problem

Go allows "naked" returns (bare `return` with no values) in functions that use
named return values. While harmless in short functions, naked returns in long
functions are a well-known readability and correctness hazard:

```go
func process(data []byte) (result *Record, err error) {
    // ... 40 lines of code ...
    result = parse(data)
    // ... more code ...
    if bad {
        return  // naked - reader must scan all named returns to know what's returned
    }
    // ...
    return  // which values are set? easy to miss one
}
```

The Go community consensus (Effective Go, Go Code Review Comments):
"Naked return is okay in (short) functions. Once a function is a few dozen
lines long, naked returns should be replaced with explicit returns."

## Gap in ggcode

No existing check detected naked returns. `go vet` and `staticcheck` do NOT
detect this pattern. `golangci-lint`'s `golint` used to warn but is deprecated.
`revive` has a nakedret rule, but it is not integrated into AI coding agents.

### Competitor Analysis

- Claude Code: no inline detection
- Cursor: lint-on-save may catch via revive, but not inline post-edit
- Cline/OpenHands: no detection
- Aider: no detection
- GitHub Copilot: no post-edit naked return analysis

## Implementation

**File**: `internal/agent/naked_return_check.go`

**Approach**: AST-based analysis. For each function with named return values,
find naked return statements (bare `return` with no values). Flag them if the
function body exceeds 20 lines (per Effective Go guidance).

### Key Design Decisions

1. **Threshold**: 20 lines (Effective Go suggests "a few dozen" but 20 is more
   conservative for AI-generated code which tends to be denser).
2. **Named returns only**: Functions without named return values cannot have
   naked returns, so they are skipped entirely.
3. **Delta-aware**: Only flags NEW naked returns introduced by this edit,
   comparing old and new content by function name.
4. **Test files skipped**: Test functions often use named returns with `t.Fatal`
   patterns that are intentional.
5. **Max 3 warnings**: Caps output to avoid flooding the user.
6. **AST traversal**: Walks all nested scopes (if/for/switch/select/blocks) to
   find bare returns.
7. **FuncDecl only**: Only checks top-level function declarations, not function
   literals (closures) inside them, to minimize false positives.

### Helper Function Prefixes

All helpers use `nr` prefix (`nrHasNamedReturns`, `nrFuncName`, `nrBodyLineCount`,
`nrFindNakedReturns`, `nrInspectStmt`) to avoid naming collisions with existing
code in the package.

## Check Registration

Registered as `naked-return` in `write_integrity.go` with `LangGo` filter.

## Files

- `internal/agent/naked_return_check.go` (check implementation)
- `internal/agent/naked_return_check_test.go` (8 tests, all passing)
- `internal/agent/write_integrity.go` (1 registration line added)
