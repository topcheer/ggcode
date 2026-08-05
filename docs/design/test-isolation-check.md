# Test Isolation Detection (Global State Mutation Guard)

## Trend

Intelligent Test Generation is a top priority in AI coding tools (GitHub Copilot Test Generation, Diffblue Cover, Pynguin, EvoSuite). A critical aspect of test quality is isolation: tests must be hermetic and not depend on or modify global state. AI agents frequently generate tests that mutate process-level state, causing flaky tests under `-parallel` execution.

## Problem

AI-generated tests commonly introduce these isolation violations:

- **os.Setenv()** instead of t.Setenv() -- permanently modifies process environment
- **os.Args mutation** -- changes the process command-line globally
- **os.Stdout/os.Stderr redirection** without restoration -- breaks output capture for other tests
- **Package-level variable writes** from Test* functions -- hidden coupling between tests

None of the competitors detect this at write time:
- **Claude Code, Cursor, Cline/OpenHands, Aider, Windsurf**: No detection.
- **go vet**: Does not flag os.Setenv or global mutations in tests.
- **staticcheck**: Does not cover test isolation.
- **testing.T.Setenv (Go 1.17+)**: Provides the safe API but does not warn when the unsafe alternative is used.

## Implementation

**Files**: `internal/agent/test_isolation_check.go`, `internal/agent/test_isolation_check_test.go`

**Check name**: `test-isolation` in the post-write integrity pipeline (`write_integrity.go`).

### How it works

1. **Scope**: Only runs on `*_test.go` files.
2. **AST analysis**: Parses Go test files to find `Test*`, `Benchmark*`, `Example*`, `Fuzz*` functions.
3. **Mutation detection**:
   - `os.Setenv()` calls inside test functions
   - Assignments to `os.Args`, `os.Stdout`, `os.Stderr`
   - Direct writes to package-level variables from test functions
4. **Delta-aware**: Only flags NEW violations introduced by the current edit (comparing old vs new content).
5. **Advisory**: Non-blocking warning with actionable remediation guidance.
6. **Zero LLM cost**: Pure AST-based pattern matching, <1ms per check.

### Detection categories

| Category | Pattern Detected | Recommendation |
|----------|-----------------|----------------|
| `os-setenv` | `os.Setenv("K", "V")` in test function | Use `t.Setenv("K", "V")` |
| `os-args` | `os.Args = ...` in test function | Save/restore with `defer` |
| `os-stdio` | `os.Stdout = ...` or `os.Stderr = ...` | Restore with `defer` |
| `global-var` | Package-level variable assignment in test function | Use local variables or `t.Cleanup` |

## Competitor Gap

No major AI coding agent (Claude Code, Cursor, Cline/OpenHands, Aider, Windsurf) detects global state mutations in tests at write time. This check provides immediate, zero-dependency feedback using only the Go stdlib AST parser.
