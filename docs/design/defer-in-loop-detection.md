# Defer-in-Loop Detection (Post-Write Check)

## Problem

AI coding agents frequently produce Go code that acquires resources (files, HTTP
bodies, mutexes) inside a for/range loop and defers cleanup with `defer f.Close()`.
While this looks correct, defer statements accumulate and only execute when the
FUNCTION returns, not when the LOOP ITERATION ends. In a loop processing N items,
this holds N file descriptors (or N mutex locks) simultaneously, causing:

- File descriptor exhaustion
- Deadlocks (mutex held across iterations)
- Memory pressure
- Intermittent production failures

This is a well-known Go anti-pattern. LLMs are prone to it because they write
the correct cleanup pattern (`defer f.Close()`) but place it in the wrong scope.

## Competitor Analysis

| Tool | Detection | Timing |
|------|-----------|--------|
| Claude Code | None (relies on golangci-lint) | N/A |
| Cursor | None (lint-on-save may catch via go vet) | Reactive |
| Cline/OpenHands | None | Reactive (tests/incidents) |
| Aider | None | N/A |
| Devin | None | N/A |
| **ggcode** | **AST-based inline detection** | **At write time (<1ms)** |

External tools (staticcheck SA4016, golangci-lint) can catch some cases but
require a separate lint cycle and are not always installed. This check provides
immediate, zero-dependency feedback using Go's standard library AST parser.

## Design

The check runs as part of the `checkWriteIntegrity` pipeline (check #19) after
every successful file write. It:

1. Parses the file using `go/parser`
2. Walks every `ForStmt` and `RangeStmt` in the AST
3. Within each loop body, recursively searches for any `DeferStmt` (at any
   nesting level through if/switch/select blocks)
4. Compares defer-in-loop counts between old and new content (delta-aware)
5. Emits warnings only for NEW defer-in-loop patterns introduced by this edit

### Key Design Decisions

- **Delta-aware**: Only flags defer-in-loop patterns that are NEW (present in
  newContent but not in oldContent). Pre-existing patterns are left alone.
- **Skips test files**: Defer-in-loop is common and often acceptable in test
  cleanup helpers.
- **AST-based**: Uses Go's standard library parser for precise detection with
  zero false positives (no regex heuristics).
- **Non-blocking**: Runs in <1ms, cannot hang (pure in-memory parsing).

## Correct Patterns

Instead of defer-in-loop, use one of these alternatives:

### Extract to helper function

```go
for _, item := range items {
    if err := processItem(item); err != nil {
        log.Println(err)
    }
}

func processItem(item string) error {
    f, err := os.Open(item)
    if err != nil {
        return err
    }
    defer f.Close() // OK: defer runs when processItem returns
    // ...
}
```

### Explicit cleanup

```go
for _, item := range items {
    f, err := os.Open(item)
    if err != nil {
        continue
    }
    // ... use f ...
    f.Close() // OK: explicit cleanup per iteration
}
```
