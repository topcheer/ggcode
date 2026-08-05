# Goroutine Recover Detection (sa-66)

## Trend: Go Concurrency Safety

Unrecovered goroutine panics are among the most dangerous Go bugs. When a
goroutine panics without recover(), the entire process crashes immediately --
there is no caller to catch the panic. This is a well-known Go anti-pattern
that affects production systems of all sizes.

## Problem

AI coding agents (Claude Code, Cursor, Cline, Aider, Devin, Windsurf) generate
Go code with inline goroutine literals (`go func() { ... }()`) without adding
recover() guards. This creates latent crash risks:

- A nil pointer dereference, type assertion failure, or slice bounds violation
  in any goroutine body crashes the whole process
- `go vet` and `staticcheck` do NOT detect missing recover() in goroutines
- Panics manifest non-deterministically depending on goroutine scheduling

## Detection Approach

AST-based analysis:
1. Walk the syntax tree for `*ast.GoStmt` nodes
2. Check if `goStmt.Call.Fun` is `*ast.FuncLit` (inline goroutine literal)
3. Search the goroutine body for any `recover()` call (including nested in defer)
4. If no recover() found, flag the goroutine

Key design decisions:
- Only flags inline goroutine literals, not `go namedFunc()` calls (the named
  function may contain its own recover)
- `hasRecoverCall` searches the entire body subtree, catching recover() at any
  nesting depth (defer wrapper, if-statement, bare call)
- Delta-aware: only flags newly introduced unrecovered goroutines
- Capped at 3 warnings per check invocation
- Skips test files (lower risk, test harness catches panics)
- Zero LLM cost -- pure AST traversal

## Relationship to Existing Checks

| Check | What it detects |
|-------|----------------|
| `panic-safety` | Bare `panic()` calls in library code |
| `goroutine-leak` | Goroutines that never terminate |
| `goroutine-recover` (NEW) | Goroutines missing recover() protection |

These three checks form defense-in-depth:
- `panic-safety`: catches the source (bare panic call)
- `goroutine-leak`: catches the lifecycle issue
- `goroutine-recover`: catches the missing safety net

## Competitor Analysis

| Product | Write-time detection |
|---------|---------------------|
| Claude Code | No |
| Cursor | No |
| Cline/OpenHands | No |
| Aider | No |
| Devin | No |
| go vet | No |
| staticcheck | No |

## Files

- `internal/agent/goroutine_recover_check.go` (implementation, ~140 lines)
- `internal/agent/goroutine_recover_check_test.go` (15 tests)
- `internal/agent/write_integrity.go` (1 line registration)
