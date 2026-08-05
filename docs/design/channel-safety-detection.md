# Channel Safety Detection - Double-Close & Send-After-Close

## Trend
**Concurrency Safety / Runtime Panic Prevention** is a critical gap in AI agent
code generation. Go's channel primitives are powerful but have runtime-safety
constraints that produce panics (not compile errors) when violated. No major
AI coding agent detects these at write time.

## Problem
AI agents frequently generate Go code with channel close misuse that causes
**runtime panics**:

1. **Double-close**: `close(ch)` called twice on the same channel in the same
   function scope → `panic: close of closed channel`
2. **Send-after-close**: `ch <- v` after `close(ch)` in the same scope →
   `panic: send on closed channel`
3. **Close-in-loop**: `close(ch)` inside a loop where `ch` was created outside
   the loop → panics on the second iteration

These bugs are insidious: they are non-deterministic (depend on goroutine
scheduling), not caught by `go vet` or `staticcheck`, and tests may pass if
timing avoids the problematic path.

## Competitor Analysis

| Product | Detection | Notes |
|---------|-----------|-------|
| Claude Code | None | Relies on agent judgment |
| Cursor | None | go vet / staticcheck don't catch these |
| Cline/OpenHands | None | Reactive only (tests/incidents) |
| Aider | None | |
| Devin | None | |
| GitHub Copilot | None | May suggest in completions |

**ggcode is the first AI coding agent to detect channel close misuse at write time.**

## Implementation

- **File**: `internal/agent/channel_safety_check.go`
- **Test**: `internal/agent/channel_safety_check_test.go` (17 tests)
- **Registration**: `write_integrity.go` - `channel-safety` check (LangGo)
- **Cost**: Zero LLM cost - pure AST analysis
- **Approach**: AST-based, delta-aware (only flags new patterns introduced by edit)

### Detection Logic

1. **Double-close**: Counts `close(ch)` calls per channel variable within a
   function scope. Flags any close beyond the first.

2. **Send-after-close**: Tracks close/send operation ordering by source
   position. Flags any send that appears after a close on the same channel.

3. **Close-in-loop**: Finds `close(ch)` inside `for`/`range` loop bodies
   where `ch` is NOT created via `make(chan...)` inside that same loop body.
   This catches the classic "close in iteration" bug.

### False Positive Mitigation

- `_test.go` files are excluded (test code close patterns are common)
- Channels created inside the loop body via `make(chan...)` are not flagged
- Delta-aware: pre-existing patterns are not re-reported
- Warning cap: 3 warnings max per check run
