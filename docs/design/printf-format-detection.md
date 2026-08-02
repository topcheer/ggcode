# Printf Format String Mismatch Detection

## Problem

AI coding agents frequently produce Go code with printf format string bugs —
the most common categories:

1. **Non-constant format argument**: `log.Printf(userInput)` where `userInput`
   is a variable. If it contains a `%` verb, the output is silently corrupted
   or the program panics with "missing argument for verb".

2. **Redundant Sprintf**: `fmt.Println(fmt.Sprintf("%d", n))` — Sprintf already
   substitutes verbs, so Println prints the result verbatim, including any
   literal `%` characters from the original. The Sprintf wrapper is pointless.

3. **Verb/argument count mismatch**: `fmt.Sprintf("%s %d", name)` — the format
   string has 2 verbs but only 1 argument. This is a `go vet` error and causes
   garbled output at runtime.

These bugs are dangerous because they're not always syntax errors — they
compile successfully and manifest as silent runtime corruption, misleading
log output, or intermittent panics. The agent then wastes iterations
debugging symptoms rather than fixing the root cause.

## Competitor Analysis

| Tool | Inline detection | Notes |
|------|-----------------|-------|
| Claude Code | No | Relies on go vet running separately |
| Cursor | Partial | Lint-on-save may catch via go vet, not at write time |
| Cline/OpenHands | No | Reactive only — caught by tests or production |
| Aider | No | No automatic detection |
| Windsurf | No | No automatic detection |
| go vet | Partial | Catches some printf arg-count issues for known functions; does NOT flag variable-as-format or redundant Sprintf |

None provide **inline detection at write time**. This check gives immediate,
zero-dependency feedback using Go's standard library AST parser (<1ms per file).

## Design

### Detection Categories

**Non-constant format argument** (`nonconstant-format`):
- The first (or second, for Fprintf) argument to a printf-family function is
  not a string literal.
- Skips all-caps identifiers (likely constants) and `fmt.Errorf(err.Error())`
  (common wrapping pattern) to reduce false positives.

**Redundant Sprintf** (`redundant-sprintf`):
- A non-format print function (Println, Print, Fatal, etc.) has a
  `fmt.Sprintf(...)` call as its first argument.

**Verb count mismatch** (`verb-count`):
- For literal format strings, counts format verbs (`%s`, `%d`, `%v`, etc.)
  and compares against the number of extra arguments.
- Handles flags (`%-5.2f`), explicit indices (`%[1]s`), and literal percents
  (`%%`) correctly.

### Printf-family functions

```
fmt.Sprintf, fmt.Errorf, fmt.Printf, fmt.Fprintf
log.Printf, log.Fatalf, log.Panicf
```

### Non-format print functions (checked for redundant Sprintf)

```
fmt.Print, fmt.Println, fmt.Fprint, fmt.Fprintln
log.Print, log.Println, log.Fatal, log.Fatalln, log.Panic, log.Panicln
```

### Delta-aware

Only flags patterns newly introduced by the edit — counts issues in both old
and new content and reports only the surplus.

## Integration

Registered as check #27 in the post-write integrity pipeline
(`write_integrity.go`). Runs synchronously after each `.go` file write/edit
alongside the other 26 checks. Non-blocking; injects a warning into the tool
result for the agent to act on in the same turn.
