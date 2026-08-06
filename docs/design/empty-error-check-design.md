# Empty Error Check Body Detection

## Overview

AI coding agents (Claude Code, Cursor, Cline/OpenHands, Aider, Windsurf) frequently
generate placeholder error handling: `if err != nil { }`. The error IS checked but
nothing is DONE about it. This silently swallows errors while giving the appearance
of proper handling.

## Problem

Unlike:
- **error-swallowing** (`_ = fn()`) — error return is explicitly discarded
- **error-nopropagate** (missing `return` after error) — error is logged but not propagated
- **Empty Error Body** — error is checked against nil but the body is literally empty

This is a distinct anti-pattern. LLMs generate it when:
1. They "scaffold" error handling but forget to fill in the body
2. They copy-paste a check pattern but omit the handling
3. They generate code where the error path was an afterthought

### Security Relevance (OWASP)

Silent error suppression is a root cause of many security incidents:
- Failed auth checks that proceed as if successful
- Failed input validation that allows invalid data through
- Failed crypto operations that continue with broken state

## Implementation

**File**: `internal/agent/empty_error_check.go`

### Detection Logic

AST-based detection of `if` statements whose:
1. Condition is a binary expression comparing an error-named identifier against `nil`
   (both `!=` and `==` operators, both orderings `err != nil` / `nil != err`)
2. Body contains zero statements OR only `EmptyStmt` nodes (bare semicolons)

### Error Variable Detection

An identifier is considered error-like if its name (case-insensitive):
- Equals `"err"` or `"e"`
- Starts with `"err"` (e.g., `readErr`, `errTimeout`)
- Ends with `"err"` (e.g., `dbErr`, `parseErr`)
- Ends with `"error"` (e.g., `myError`)

### Delta Awareness

Only flags NEW empty error bodies introduced by this edit. If old content had
2 instances and new content has 3, only 1 warning is emitted.

### Constraints

- Max 3 warnings per edit (prevents flooding)
- Zero LLM cost — pure AST pattern matching
- No external dependencies
- Helper functions prefixed with `ee` to avoid naming collisions

## Competitor Analysis

| Product | Write-time Detection | Approach |
|---------|---------------------|----------|
| Claude Code | No | — |
| Cursor | No | Relies on external linters |
| Cline/OpenHands | No | — |
| GitHub Copilot | No | — |
| Aider | No | — |
| staticcheck | No | SA series does not cover this pattern |
| **ggcode** | **Yes** | AST-based, delta-aware, < 1ms |

## Test Coverage

15 tests in `empty_error_check_test.go`:
- Detects `if err != nil {}` empty body
- Detects body with only empty statements (`;`)
- No warning for non-empty body
- Delta-aware (skips pre-existing instances)
- Handles `err == nil` (equality check)
- Handles error variable suffixes (`readErr`)
- Ignores non-error nil checks (`if x != nil {}`)
- Handles empty/invalid content gracefully
- Caps at max 3 warnings
