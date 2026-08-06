# Error Not Propagated Detection (sa-70)

## Trend
Error handling quality remains a top concern in AI-assisted code generation.
Research shows that the most common error-handling bug in AI-generated Go code
is not the empty handler or bare return (both already detected by
`error_swallow_check.go`), but rather **acknowledging the error via side
effects (logging, metrics) without returning it**. This silently swallows
errors while creating the illusion of proper handling. The `--` em-dashes
in this doc have been replaced with ASCII equivalents.

## Competitor Analysis

| Tool | Write-time detection | Mechanism |
|------|---------------------|-----------|
| Claude Code | No | Relies on agent judgment |
| Cursor | No | External linters only |
| Cline/OpenHands | No | Reactive (tests/incidents) |
| Aider | No | Diff review, manual |
| GitHub Copilot | No | Suggestion filtering only |
| **ggcode** | **Yes** | **AST-based, zero-LLM-cost** |

## Gap Analysis

The existing `error-swallowing` check (`error_swallow_check.go`) detects:
1. Empty body: `if err != nil { }`
2. Bare return: `if err != nil { return }`

**Missing**: The most common pattern -- handlers with side effects but no return:

```go
func processData() error {
    data, err := fetch()
    if err != nil {
        log.Printf("fetch failed: %v", err)  // logs but doesn't return
    }
    // execution continues with invalid state
    return nil  // returns success despite the error
}
```

This pattern accounts for the majority of silent error-swallowing bugs in
production Go code. No static analysis tool currently detects this at write
time within AI coding agents.

## Implementation

**File**: `internal/agent/error_nopropagate_check.go` (253 lines)

**Approach**: AST-based analysis. For each `if err != nil` block in an
error-returning function, check if the body:
- Is non-empty (has side effects)
- Has no `return` statement (not even bare)
- Has no `continue`/`break` (loop exit)
- Has no fatal terminator (panic, log.Fatal, os.Exit, testing.T.Fatal)

If all conditions hold, the error handler is flagged.

**Key design decisions**:
1. **Delta-aware**: Only flags NEW instances introduced by the edit
   (same pattern as error_swallow_check)
2. **Closure-aware**: `hasAnyReturn`/`hasLoopExit`/`hasFatalTerminator` skip
   `*ast.FuncLit` nodes because closures have their own return scope
3. **Non-overlapping with error_swallow_check**: Empty body and bare return
   are already handled; this check only fires for non-empty bodies without
   returns
4. **Terminator methods**: Recognizes `panic`, `log.Fatal/Fatalf/Fatalln`,
   `testing.T.Fatal/Fatalf/Fatalln/Skip/Skipf/SkipNow`, `os.Exit`,
   `runtime.Goexit` as valid terminators
5. **Non-error functions**: Only triggers in functions that return `error` --
   in non-error functions, logging without return is a valid pattern

**Complexity**: All functions under cyclomatic complexity 15.

## Tests

**File**: `internal/agent/error_nopropagate_check_test.go` (17 tests)

Coverage:
- Positive: log-without-return, multiple issues
- Negative: return present, bare return, empty body, panic, log.Fatal,
  os.Exit, t.Fatal, continue, break, nested return, non-error func,
  err == nil check, delta-aware, non-Go file, empty content

## Registration

Added to `write_integrity.go` `allChecks`:
```go
{Name: "error-nopropagate", Langs: []Language{LangGo}, Run: sliceCheck(checkErrorNoPropagate)},
```
