# Background Pre-Compaction Design

## Problem

When token count exceeds `autoCompactThreshold`, `maybeAutoCompact` runs synchronously inside `RunStreamWithContent`, making a full LLM call (`prov.Chat`) to summarize history. This blocks the user for **2-30 seconds** before their prompt is even sent to the LLM.

The key insight: after an agent run completes, the agent is idle until the user types the next message. This idle time (typically 5-300+ seconds) is wasted. We can use it to pre-compact in the background.

## Design

### Core Idea

```
Agent run completes → immediately kick off background compaction goroutine
                       ↓
User types next prompt → check if pre-compaction result is ready:
                         ├─ Ready → apply result instantly, skip inline compact
                         └─ Not ready → wait for it (with timeout) or proceed without
```

### Three States

```
  IDLE ──→ COMPACTING ──→ COMPACTED
    ↑                               │
    └───────────────────────────────┘
         (consumed by next RunStreamWithContent)
```

---

## Implementation Plan

### 1. New file: `internal/agent/agent_precompact.go`

```go
package agent

import (
    "context"
    "sync"
    "time"

    "github.com/topcheer/ggcode/internal/debug"
    ctxpkg "github.com/topcheer/ggcode/internal/context"
    "github.com/topcheer/ggcode/internal/provider"
)

// precompactState tracks an in-flight background compaction.
//
// Lifecycle:
//   Agent run ends → StartPreCompact() creates precompactState, starts goroutine
//   Next RunStreamWithContent → waitForPreCompact(ctx) waits on .done
//   Goroutine finishes → closes .done, stores .err
//   waitForPreCompact returns → clears a.precompact
type precompactState struct {
    done   chan struct{}    // closed when compaction completes (success or failure)
    cancel context.CancelFunc // cancels the pre-compact's own 60s context
    err    error           // non-nil if compaction failed
}

// StartPreCompact initiates a background compaction if conditions warrant it.
// It returns immediately and runs the compaction in a goroutine.
// The goroutine uses its own 60s timeout context, independent of any user request ctx.
func (a *Agent) StartPreCompact() {
    a.mu.Lock()
    // Don't start if one is already running
    if a.precompact != nil {
        a.mu.Unlock()
        return
    }
    // Only compact if we're approaching the threshold
    tokens := a.contextManager.TokenCount()
    threshold := a.contextManager.AutoCompactThreshold()
    if threshold <= 0 || tokens < threshold {
        a.mu.Unlock()
        debug.Log("precompact", "SKIP: tokens=%d threshold=%d", tokens, threshold)
        return
    }

    // Own context — not tied to any user request
    bgCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    a.precompact = &precompactState{
        done:   make(chan struct{}),
        cancel: cancel,
    }
    a.mu.Unlock()

    debug.Log("precompact", "START: tokens=%d threshold=%d", tokens, threshold)

    go func() {
        defer close(a.precompact.done)

        prov := a.Provider()
        if prov == nil {
            a.precompact.err = fmt.Errorf("no provider")
            return
        }

        changed, err := a.contextManager.CheckAndSummarize(bgCtx, prov)
        if err != nil {
            a.precompact.err = err
            debug.Log("precompact", "FAILED: %v", err)
            return
        }
        if changed {
            a.maybeSaveCheckpoint()
        }
        newTokens := a.contextManager.TokenCount()
        debug.Log("precompact", "DONE: %d → %d tokens", tokens, newTokens)
    }()
}

// waitForPreCompact waits for a running background compaction to finish.
// It respects the caller's ctx (user can ctrl+c to stop waiting).
// Returns true if a pre-compact was in progress (resolved or still running).
// Returns false if no pre-compact was running.
func (a *Agent) waitForPreCompact(ctx context.Context) bool {
    a.mu.RLock()
    pc := a.precompact
    a.mu.RUnlock()

    if pc == nil {
        return false
    }

    debug.Log("precompact", "WAITING for background compaction to finish")
    select {
    case <-pc.done:
        // Pre-compact finished. Clear state and return.
        debug.Log("precompact", "WAIT resolved, err=%v", pc.err)
        a.mu.Lock()
        a.precompact = nil
        a.mu.Unlock()
        return true
    case <-ctx.Done():
        // User cancelled (ctrl+c) or external context cancelled.
        // Do NOT clear precompact state — let the background goroutine finish
        // and store its result. The next RunStreamWithContent call will pick it up.
        debug.Log("precompact", "WAIT cancelled by ctx: %v", ctx.Err())
        return true // still signal that a pre-compact was attempted
    }
}
```

### 2. Add field to Agent struct (`agent.go`)

```go
type Agent struct {
    // ... existing fields ...
    precompact *precompactState // background pre-compaction state
}
```

### 3. Trigger pre-compact when agent run completes (`internal/tui/submit.go`)

In the `startAgentWithExpand` goroutine, after the agent run finishes:

```go
func (m *Model) startAgentWithExpand(text string) tea.Cmd {
    // ... existing setup ...

    return func() tea.Msg {
        go func() {
            defer func() {
                if m.program != nil {
                    m.program.Send(agentDoneMsg{RunID: runID})
                }
                cancel()
            }()

            // ... existing ExpandMentions + runAgentSubmission ...

            // After agent run completes, kick off background pre-compact
            if m.agent != nil {
                m.agent.StartPreCompact()
            }
        }()

        return nil
    }
}
```

### 4. Use pre-compact result in RunStreamWithContent (`internal/agent/agent.go`)

Replace the synchronous `maybeAutoCompact` at line 336:

```go
func (a *Agent) RunStreamWithContent(ctx context.Context, content []provider.ContentBlock, onEvent func(provider.StreamEvent)) error {
    a.contextManager.Add(provider.Message{
        Role:    "user",
        Content: content,
    })

    transientCompactWarned := false

    // NEW: Wait for background pre-compact if one is running.
    // Uses ctx from the user's request — user can ctrl+c to cancel the wait.
    // If pre-compact already finished, this returns instantly.
    if a.waitForPreCompact(ctx) {
        // Pre-compact finished or user cancelled the wait.
        // maybeAutoCompact below will decide if further compact is needed.
        // If pre-compact succeeded: tokens < threshold → maybeAutoCompact is a no-op.
        // If pre-compact failed or user cancelled: maybeAutoCompact tries synchronously.
    }

    if err := a.maybeAutoCompact(ctx, onEvent, &transientCompactWarned); err != nil {
        onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
        return err
    }

    // ... rest unchanged ...
}
```

### 5. Edge case: user sends prompt before pre-compact finishes

```
Timeline:
  t=0s   Agent run completes → StartPreCompact() kicks off
  t=1s   User types next prompt → RunStreamWithContent starts
  t=1s   waitForPreCompact(ctx) blocks, respecting user's ctx
  t=5s   Pre-compact finishes → waitForPreCompact returns
  t=5s   maybeAutoCompact → SKIP (already compacted)
  t=5s   LLM call proceeds immediately
```

No hardcoded timeout. The wait uses the same `ctx` as the agent run:
- **User can ctrl+c** to cancel the wait at any time
- **No artificial deadline** — waits exactly as long as the pre-compact needs
- If ctx is cancelled while waiting: `waitForPreCompact` returns, the background goroutine continues and finishes, its result is available for the *next* `RunStreamWithContent` call
- The `precompact` state is NOT cleared on ctx cancellation, so the background goroutine's work is never wasted

### 6. Edge case: user ctrl+c while waiting for pre-compact

```
Timeline:
  t=0s   StartPreCompact() kicks off
  t=1s   User sends prompt → waitForPreCompact(ctx) starts waiting
  t=3s   User presses ctrl+c → ctx.Done() fires
  t=3s   waitForPreCompact returns true (ctx cancelled)
  t=3s   RunStreamWithContent returns ctx.Err()
  t=5s   Background pre-compact finishes anyway (its own 60s ctx)
  ...next prompt sees precompact != nil, already done → applies instantly
```

Key: the background pre-compact goroutine uses its own `context.WithTimeout(60s)`, independent of the user's request ctx. So it always finishes, even if the user cancels.

---

## Concurrency Safety Analysis

### Two independent contexts

```
User request ctx (from startAgentWithExpand):
  - context.WithCancel(context.Background())
  - Cancelled when user presses ctrl+c
  - Used by: RunStreamWithContent, streamChatResponse, maybeAutoCompact, waitForPreCompact

Pre-compact ctx (from StartPreCompact):
  - context.WithTimeout(context.Background(), 60s)
  - Independent of any user request
  - Used by: CheckAndSummarize → summarizeMessages → prov.Chat
```

These are deliberately decoupled:
- Pre-compact always runs to completion (up to 60s) even if user cancels their request
- User cancelling doesn't abort the compaction — its work is preserved for the next turn
- `waitForPreCompact` bridges the two: it waits on pre-compact's `done` channel but respects the user's `ctx` for the *wait itself*

### contextManager.Add() during background Summarize()

The existing `Summarize()` already handles this via TOCTOU fix at `manager.go:260-265`:

```go
// Collect any messages that arrived during summarization (TOCTOU fix)
var extraMsgs []provider.Message
if len(m.messages) > plan.origLen {
    extraMsgs = make([]provider.Message, len(m.messages)-plan.origLen)
    copy(extraMsgs, m.messages[plan.origLen:])
}
```

This means:
- Pre-compact runs `CheckAndSummarize()` which calls `buildSummaryPlan()` (snapshot under lock)
- Then `summarizeMessages()` (slow LLM call, no lock held)
- Then applies the result under lock, merging any new messages that arrived during the LLM call
- **No new messages can arrive** during pre-compact because the agent run is over and the user hasn't typed yet. This makes the TOCTOU window even smaller.

### Race with user starting new conversation / clearing context

If the user switches sessions or clears the conversation while pre-compact is running:
- The `contextManager` is replaced (`Clear()` or new `Manager`)
- The goroutine's reference to the old `contextManager` operates on stale data
- The new `RunStreamWithContent` creates a new `precompact` state

**Fix:** Call `CancelPreCompact()` when the session changes or context is cleared:

```go
func (a *Agent) CancelPreCompact() {
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.precompact != nil && a.precompact.cancel != nil {
        a.precompact.cancel()
    }
    // Don't nil out a.precompact — let the goroutine close the done channel
    // so waitForPreCompact doesn't block forever on a stale reference.
    // The goroutine will see its context cancelled and exit quickly.
}
```

This also means `waitForPreCompact` must handle the case where `precompact.done` is closed but the compaction was cancelled:

```go
select {
case <-pc.done:
    if pc.err != nil {
        debug.Log("precompact", "WAIT resolved but failed: %v", pc.err)
    }
    a.mu.Lock()
    a.precompact = nil
    a.mu.Unlock()
    return true
case <-ctx.Done():
    // User cancelled the wait. Pre-compact keeps running with its own ctx.
    debug.Log("precompact", "WAIT cancelled by ctx: %v", ctx.Err())
    return true
}
```

---

## Files to Modify

| File | Change |
|------|--------|
| `internal/agent/agent.go` | Add `precompact *precompactState` field; use `waitForPreCompact` in `RunStreamWithContent` |
| `internal/agent/agent_precompact.go` | **New file**: `StartPreCompact()`, `waitForPreCompact()`, `CancelPreCompact()` |
| `internal/tui/submit.go` | Call `m.agent.StartPreCompact()` after `runAgentSubmission` completes |
| `internal/context/manager.go` | No changes needed (already TOCTOU-safe) |

---

## Expected Impact

| Scenario | Before | After |
|----------|--------|-------|
| First prompt ever | ~10ms pre-LLM | ~10ms (no change) |
| 10th prompt, no compact needed | ~10ms | ~10ms (no change) |
| 15th prompt, compact triggered | **2-30s inline blocking** | ~0ms inline (done in background during idle) |
| Fast user (types while compact runs) | N/A | Waits for compact to finish; user can ctrl+c to cancel wait |
| User ctrl+c while waiting for compact | N/A | Wait cancelled, agent run cancelled; pre-compact finishes in background, ready for next turn |
| Compact not needed but close to threshold | ~10ms | Pre-compact kicks in during idle, ready for next time |

**Net effect:** For typical usage (5-30s between prompts), the compaction is always done by the time the user sends their next message. The user never perceives the 2-30s compaction delay.
