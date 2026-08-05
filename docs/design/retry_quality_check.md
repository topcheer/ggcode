# Retry Loop Quality Detection -- Resilience Engineering Intelligence Check

## SA-62: Retry Quality (Error Recovery & Resilience)

**Status**: Implemented
**Date**: 2025
**Direction**: Error Recovery & Resilience
**Files**: `retry_quality_check.go`, `retry_quality_check_test.go`, 1 line in `write_integrity.go`

---

## Problem

AI coding agents frequently generate retry/reconnect loops that lack critical resilience
properties, causing production incidents in two distinct failure modes:

### Failure Mode 1: Missing Backoff (Retry Storms)

```go
// BAD: re-attempts immediately on every failure
for {
    resp, err := http.Get(url)
    if err != nil {
        continue  // <-- no delay: retry storm / thundering herd
    }
    break
}
```

When a downstream service degrades, thousands of clients retrying with zero delay
amplify the outage (cascading failure). This is the #1 cause of cascading outages
documented in Google SRE's "Cascading Failures" chapter and AWS's Well-Architected
Reliability pillar.

### Failure Mode 2: Missing Attempt Cap (Infinite Loops)

```go
// BAD: for {} never terminates if target stays down
for {
    err := db.Ping()
    if err != nil {
        continue  // <-- no max attempts: goroutine leak, CPU burn
    }
    break
}
```

An unbounded `for {}` retry loop leaks goroutines and burns CPU forever when the
target stays down -- a common source of silent resource exhaustion.

---

## Competitor Analysis

| Tool | Retry backoff detection | Unbounded retry detection | Write-time inline? |
|------|:-:|:-:|:-:|
| **ggcode (this check)** | **Yes** | **Yes** | **Yes** |
| Claude Code | No | No | No (relies on external linters) |
| Cursor / Windsurf | No | No | No |
| Cline / OpenHands | No | No | Reactive only |
| Devin | No | No | No |
| Aider | No | No | No |
| gosec (G107/G112) | No | No | SSRF/SSRF only |
| staticcheck (S1000-S1040) | No | No | Style/perf only |
| revive | No | Partial (unconditional-recursion: recursion, not retry) | Lint phase |

**Gap**: No major AI coding agent or Go linter detects retry-loop resilience bugs
at write time. They are discovered only in production incidents or manual review.

---

## Approach

AST-based analysis of Go source (`go/ast` + `go/parser`). Zero LLM cost, <1ms per file.

### Classification: What is a "retry loop"?

A `for` loop is classified as a retry loop when its body contains BOTH:
1. **A failing call** -- a network/IO call that returns an error (http.Get, db.Exec,
   db.Ping, net.Dial, etc. -- 15 common function names tracked).
2. **Error-driven continuation** -- an `if err != nil { continue }` pattern (the
   hallmark of retry-on-failure semantics).

Only loops matching both criteria are flagged, minimizing false positives. A plain
iteration loop without error checks is never flagged.

### Detection: Two independent checks

Once classified as a retry loop:

1. **Missing backoff**: the loop body contains no `time.Sleep`, `time.After`, or
   `time.Tick` call → no delay between attempts → retry storm risk.

2. **Unbounded retry**: the loop is `for {}` (no condition) AND has no attempt
   counter (neither in the for-clause nor compared in the body) → infinite loop risk.

A well-formed retry with backoff and a counter (`for attempt := 0; attempt < max;
attempt++`) passes both checks silently.

### Delta-aware

Only patterns **newly introduced** by the current edit are reported. Pre-existing
issues in the old content are subtracted via AST comparison, so editing a file
with an old retry bug doesn't re-report it on every save.

---

## Design Decisions

1. **Dual classification gate** (failing call + error-continue): prevents false
   positives on iteration loops, range loops, and non-error control flow.

2. **Backoff detection includes `time.After` in select statements**: a select with
   `case <-time.After(...)` is a valid backoff pattern (common in production code).

3. **Attempt-cap detection is multi-path**: recognizes (a) for-clause counters
   (`for i := 0; i < max; i++`), (b) for-init assignment of a counter, and (c)
   body-level counter comparisons (`if attempt >= maxRetries`). This covers the
   three idiomatic Go retry-loop shapes.

4. **Counter name heuristic**: `attempt`, `retry`, `retries`, `count`, `tries`
   substrings (case-insensitive) classify a variable as an attempt counter. This
   is deliberately broad to catch varied naming.

5. **Error ident heuristic**: `err` and `e` (case-insensitive) classify error
   variables -- the two overwhelmingly common Go error variable names.

6. **Naming collision avoidance**: helpers prefixed `retry*` (`retryCallFuncName`,
   `retryIsErrIdent`, `retryIsNilLit`) to avoid collisions with existing checks
   (`nplus1_loop_check.go`, `nil_deref_check.go`).

7. **Cyclomatic complexity**: all functions kept well under 15 (max is ~8).

---

## Test Coverage

12 tests covering:

| Test | Scenario | Expected |
|------|----------|----------|
| MissingBackoff | retry loop, no delay | warn |
| UnboundedRetry | `for {}` with continue | warn |
| HasBackoffAndCap_OK | backoff + counter | no warn |
| UnboundedButHasCounterCheckInBody | body-level counter compare | no unbounded warn |
| NotRetryLoop_NoWarning | plain call, no loop | no warn |
| DeltaAware | same content old+new | no warn |
| NonGoFile | .py file | nil |
| EmptyContent | empty string | nil |
| SyntaxError | broken parse | nil |
| DBRetryMissingBackoff | db.Ping retry | warn |
| HasBackoffViaTimeAfter | select with time.After | no warn |
| BoundedLoopNoErrCheck_NotRetry | bounded loop, no err check | no warn |

All 12 pass.

---

## Constraints Met

- [x] No existing behavior modified (only new files + 1 registration line)
- [x] Minimal scope
- [x] All functions cyclomatic complexity < 15
- [x] Zero LLM cost (pure AST)
- [x] No new external dependencies
- [x] write_integrity_test.go untouched
