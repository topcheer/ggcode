# Infinite Loop Without Exit Detection

## Problem

AI coding agents sometimes emit `for {}` loops with no exit path. These compile
cleanly but hang forever at runtime, causing deadlocks, goroutine leaks, or
process hangs.

## Detection

Flags `for {}` (ForStmt with Cond == nil, no range) whose body contains no
exit statement at any nesting level.

Recognized exit statements:
- `break`, `goto`
- `return`
- `panic()`
- `os.Exit()`, `runtime.Goexit()`
- `log.Fatal/Fatalf/Fatalln/Panic/Panicf/Panicln`

## Files

- `internal/agent/infinite_loop_check.go` - check implementation
- `internal/agent/infinite_loop_check_test.go` - 14 tests
- `internal/agent/write_integrity.go` - registration (1 entry)

## Approach

AST-based, zero LLM cost. Uses `ast.Inspect` to recursively search the loop
body for exit statements. Conservative: if any exit exists at any depth, the
loop is not flagged. Capped at 4 warnings per file.
