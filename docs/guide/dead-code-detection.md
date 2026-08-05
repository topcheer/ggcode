# Dead Code Detection (Write-Time Intelligence)

## Overview

ggcode detects dead code patterns at **write time** - the moment the agent edits a Go file - providing immediate feedback before any external linter runs. This is a deterministic, zero-LLM-cost check based on AST pattern matching.

## What It Detects

The dead-code check (`internal/agent/dead_code_check.go`) complements the existing [unreachable-code](unreachable-code-detection.md) check with four additional patterns:

### 1. Empty Branch Bodies

Flags `if`, `for`, `switch`, and `range` blocks with no executable statements.

```go
// Warning: Empty if body
if err != nil {
}
```

Common when an agent removes code from a branch but leaves the branching structure behind.

### 2. Empty Function Bodies

Flags non-test, non-`init()`/`main()` functions with empty bodies and no comments.

```go
// Warning: Empty function body for unimplemented
func unimplemented() {
}
```

Indicates incomplete or abandoned implementations left by the agent.

### 3. Dead Assignments

Flags variables assigned a value that is overwritten before being read (ineffassign pattern).

```go
// Warning: Dead assignment - x is overwritten before being read
x := computeSomething()
x = 42
```

### 4. Unused Function Parameters

Flags function parameters that are declared but never referenced in the body (varcheck/U1000 pattern).

```go
// Warning: Unused parameter "count"
func process(data string, count int) string {
    return data
}
```

## What It Skips

- **Test files** (`_test.go`): empty bodies and unused params are common in test stubs
- **`init()` and `main()`**: can legitimately be empty
- **Functions with comments**: a body with `// TODO` is not flagged
- **Underscore parameters** (`_`): intentional discards

## Integration

Registered as `"dead-code"` in the post-write integrity check pipeline (`write_integrity.go`). Runs for Go files only, in parallel with other checks via the check registry framework.

- **Max warnings per write**: 4
- **Cost**: Zero LLM cost (deterministic AST analysis)
- **Delta-aware**: Parses with `parser.ParseComments` for accurate comment detection

## Comparison with External Tools

| Feature | ggcode (write-time) | staticcheck (CI) | golangci-lint (CI) |
|---------|:---:|:---:|:---:|
| Empty branches | Yes | No | No |
| Empty function bodies | Yes | No | No |
| Dead assignments | Yes | No | Yes (ineffassign) |
| Unused parameters | Yes | Yes (U1000) | Yes (varcheck) |
| Timing | Write-time | CI | CI |
| LLM cost | Zero | N/A | N/A |

ggcode provides **immediate feedback** in the same iteration, while external tools only catch issues at CI time.
