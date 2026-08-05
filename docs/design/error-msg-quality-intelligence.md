# Error Message Quality Intelligence

## Overview

This check detects low-quality error messages in Go code at write time, providing immediate, zero-LLM-cost feedback via AST analysis. It complements existing error-handling checks (`error_wrap_check`, `error_swallow_check`, `ignored_error_check`) by focusing on the TEXT of the error message itself rather than structural patterns.

## Problem

AI coding agents frequently produce Go code with low-quality error messages that waste debugging time:

1. **Empty messages**: `errors.New("")` or `fmt.Errorf("")` - zero debugging context
2. **Generic messages**: `errors.New("error")`, `errors.New("failed")`, `errors.New("something went wrong")` - no actionable information
3. **Context-free wrapping**: `fmt.Errorf("%w", err)` - wraps an error with no additional context

These are quality issues, not bugs - the code compiles and works, but debugging production incidents becomes significantly harder.

## What This Check Detects

### Pattern 1: Empty Error Message
```go
// Bad - no debugging context
return errors.New("")
return fmt.Errorf("")

// Good - describes what happened
return errors.New("config file not found")
return fmt.Errorf("failed to parse config %s", path)
```

### Pattern 2: Generic Error Message
```go
// Bad - conveys no actionable information
return errors.New("error")
return errors.New("failed")
return errors.New("something went wrong")
return errors.New("unexpected error")

// Good - identifies the operation and failure reason
return errors.New("database connection refused")
return fmt.Errorf("failed to parse config %s: %w", path, err)
```

### Pattern 3: Context-Free Wrapping
```go
// Bad - adds nothing the original error didn't have
return fmt.Errorf("%w", err)

// Good - includes the current operation context
return fmt.Errorf("parsing config: %w", err)
```

## Implementation

- **File**: `internal/agent/error_msg_quality_check.go`
- **Registration**: `write_integrity.go` as `error-msg-quality` check
- **Language filter**: Go only (`LangGo`)
- **Test files**: Excluded (`_test.go` suffix check)
- **Approach**: AST-based analysis of `errors.New` and `fmt.Errorf` calls
- **Delta-aware**: Only flags newly-introduced patterns
- **Capped**: Maximum 3 warnings per write
- **Zero LLM cost**: Deterministic pattern matching

## Generic Message Set

The check uses a curated set of 30+ low-information messages matched case-insensitively after trimming whitespace and trailing punctuation. Examples include: `error`, `failed`, `oops`, `something went wrong`, `an error occurred`, `unexpected error`, `internal error`, `unknown error`, `not implemented`, `todo`, `fixme`, `placeholder`.

## Competitor Analysis

| Tool | Detection | Limitation |
|------|-----------|------------|
| Semgrep | Rules for empty error messages | Requires external lint cycle |
| SonarQube | Some generic message detection | Not at write time |
| CodeRabbit | PR review error quality comments | Reactive (post-PR) |
| Claude Code | No detection | - |
| Cursor | No detection | - |
| go vet | No error message quality check | - |
| staticcheck | No generic message flagging | - |

No major AI coding agent provides inline detection of error message quality at write time.

## Tests

- **File**: `internal/agent/error_msg_quality_check_test.go`
- **17 tests** covering: non-Go files, test files, empty content, empty messages (errors.New/Errorf), generic messages (multiple variants), context-free wrapping, good messages (no false positives), delta-awareness, non-literal messages, max warnings cap, case insensitivity, trailing punctuation.
