# Type Switch Exhaustiveness Detection

## Overview

Intelligence check (sa-65) that detects Go type switches on interfaces lacking a `default` case. This is a common source of silent failures in AI-generated code.

## Problem Statement

AI coding agents frequently write type switches without a default branch:

```go
func handleErr(err error) string {
    switch e := err.(type) {
    case *ValidationError:
        return e.Field
    case *AuthError:
        return e.Reason
    }
    // Missing default - unknown types silently return zero-value ""
}
```

When a new concrete type is added to the type system (e.g., `*TimeoutError`), the switch silently does nothing for that type. This causes:
1. **Silent failures**: function returns zero-value with no error signal
2. **Debugging difficulty**: missing branch is invisible in production logs
3. **Security risks**: error type switches that skip unknown types may mask auth/validation failures

## Competitor Analysis

| Tool | Write-time Detection | Notes |
|------|---------------------|-------|
| Claude Code | No | Relies on agent judgment |
| Cursor | No | go vet does not flag this |
| Cline/OpenHands | No | No detection |
| Aider | No | No detection |
| Windsurf | No | No detection |
| Devin | No | No detection |
| staticcheck | No | Does not flag missing type switch cases |
| exhaustive (3rd-party) | Partial | Only checks enum switch statements, not interface type switches |

**Gap**: No AI coding agent provides inline write-time detection of incomplete type switches. The popular `exhaustive` linter only handles enum-to-string switches, not interface type switches.

## Implementation

**File**: `internal/agent/type_switch_check.go` (175 lines)
**Tests**: `internal/agent/type_switch_check_test.go` (12 tests)
**Registration**: 1 line in `write_integrity.go`

### Detection Logic

1. Parse Go source via AST
2. Walk all `*ast.TypeSwitchStmt` nodes
3. For each switch, count type cases and check for default (empty `cc.List`)
4. Flag switches with 2+ type cases and no default
5. Delta-aware: suppress warnings for switches that existed in old content

### Key Design Decisions

1. **Function-name-based dedup key**: Uses the enclosing function name instead of source position for delta-aware filtering. This is robust against line shifts when new declarations are added above the switch.

2. **Minimum 2 cases threshold**: Single-case type switches are often intentional "is this type X?" checks and don't warrant a default. Two+ cases indicate exhaustive intent.

3. **Test file exclusion**: `_test.go` files are skipped since test code often intentionally ignores types for brevity.

4. **Zero LLM cost**: Pure AST analysis with no external dependencies.

## Verification

- All 12 tests pass
- Full CI verification passes (`VERIFY_CI_MEMLIMIT=256MiB make verify-ci`)
- Build clean with `-tags goolm`
- No new external dependencies
