# Goroutine Lifecycle Leak Detection (Post-Write Check)

## Problem

AI coding agents frequently produce Go code that spawns goroutines via
`go func()` or `go someFunc()` without any mechanism to track, cancel, or
wait for their completion. This causes **goroutine leaks** -- the goroutines
outlive the function that spawned them, holding references to resources and
preventing garbage collection.

In production, goroutine leaks manifest as:
- Slow, steady memory growth
- File descriptor exhaustion
- Mysterious hangs when shutdown logic is never reached
- Connection pool starvation

## Gap in Existing Checks

The existing `resource_leak_check.go` detects resource acquisitions
(`os.Open`, `http.Get`, `net.Listen`) without matching `defer Close()`.
The `lock_without_unlock_check.go` detects mutex deadlocks. Neither detects
**goroutine lifecycle problems** -- a function can have all resources properly
closed and all locks properly unlocked, yet still leak goroutines.

## Competitor Analysis

- **Claude Code**: no automatic detection (relies on external linters)
- **Cursor**: no automatic detection (go vet doesn't catch this)
- **Cline/OpenHands**: reactive only -- caught by tests or production incidents
- **Aider**: no automatic detection
- **Windsurf**: no automatic detection

`go vet` does NOT detect goroutine leaks. `staticcheck` doesn't have a rule
for this. The `go.uber.org/goleak` package detects leaks at test time but
requires actual execution and test infrastructure.

## Approach

AST-based analysis of Go functions. For each function:

1. Find all `go` statements (`go func()`, `go someFunc()`)
2. Check whether the spawning function has **any** goroutine lifecycle management:
   - `sync.WaitGroup` (methods: `Add`, `Done`, `Wait`)
   - `context.WithCancel`, `context.WithTimeout`, `context.WithDeadline`
   - `errgroup.WithContext`
   - `close(ch)` -- channel close as a shutdown signal
   - Channel send to signal channels (`stop`, `done`, `quit`, `shutdown`, `cancel`)
3. If NO lifecycle mechanism is found, flag all `go` statements in the function

### False Positive Mitigation

- Functions using **any** lifecycle mechanism (even partially) are not flagged
- `main()` and `init()` are excluded -- they are entry points, not expected
  to join goroutines
- Channel send detection is conservative: only channels named with signal-like
  keywords (`stop`, `done`, `quit`, etc.) are treated as lifecycle management

Only **new** instances introduced by this edit are flagged (delta-aware),
using the same pattern as other post-write checks.

## Registration

Check #32 in the post-write integrity pipeline (`write_integrity.go`).
