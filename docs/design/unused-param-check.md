# Unused Function Parameter Detection

## Check ID: `unused-param`

## Category
Code Quality / Dead Code

## Problem
Function parameters that are never referenced in the function body often indicate:
- Copy-paste errors (wrong function signature copied)
- Incomplete refactoring (parameter left behind after removal)
- Dead code accumulation
- Interface compliance placeholders no longer needed

Unlike Go's compiler which enforces unused *local variables*, unused *function parameters* produce no warning. Linters like `staticcheck` (U1000) catch this but only with full type-checking context.

## Detection
Pure AST-based, zero LLM cost. For each unexported function with a body:
1. Walk the function body and collect all referenced identifiers into a set
2. For each named parameter, check if it appears in the used set
3. Flag parameters not found, excluding `_` (blank identifier)

### Heuristics to reduce false positives
- **Skip exported functions**: Public API may require parameters for interface stability
- **Skip test files**: Test fixtures commonly have unused parameters
- **Skip stubs**: Functions with <= 1 statement are too early-stage
- **Skip blank identifier**: `_` is intentionally unused by convention
- **Skip functions with no body**: Forward declarations, interface stubs

## Language
Go only (`LangGo`)

## Files
- `internal/agent/unused_param_check.go` -- check implementation (88 lines)
- `internal/agent/unused_param_check_test.go` -- 10 tests
- `internal/agent/write_integrity.go` -- registration (1 entry)

## Complexity
All functions under cyclomatic complexity 8.

## Competitor Gap
No major AI coding agent (Claude Code, Cursor, Copilot, Aider) performs write-time
unused parameter detection. Static analysis tools like staticcheck catch this only
with full package type-checking, not at edit time.
