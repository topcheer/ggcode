package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

// precompactState tracks an in-flight background pre-compaction.
//
// Lifecycle:
//
//	idle ──► StartPreCompact() creates state, launches goroutine ──► COMPACTING
//	COMPACTING ──► goroutine writes err then closes done ──► DONE
//	next RunStreamWithContent ──► consumeReadyPreCompact applies if done ──► cleared
//
// All field reads on this struct are guarded by either:
//   - holding Agent.mu (for the *precompactState pointer itself), or
//   - having observed the close of `done` (happens-before via channel close).
type precompactState struct {
	done      chan struct{}          // closed when the goroutine exits
	cancel    context.CancelFunc     // cancels this pre-compact's own bgCtx
	startedAt time.Time              // when the goroutine started; for status display
	startTok  int                    // token count snapshot when started
	snapshot  ctxpkg.CompactSnapshot // immutable live-context snapshot compacted in background
	result    ctxpkg.CompactResult   // populated before close(done) — read only after <-done
	err       error                  // populated before close(done) — read only after <-done
	cancelled bool                   // set if CancelPreCompact was called externally
}

const (
	precompactBackgroundTimeout = 180 * time.Second
	// precompactStartDelay staggers the compression LLM call away from the
	// agent's regular LLM turn to avoid API rate-limit collisions.
	precompactStartDelay = 6 * time.Second

	// toolResultClearTrigger starts proactive tool-result clearing at this
	// fraction of the compaction threshold. Below this, clearing is unnecessary.
	// This is the "tool-result clearing" technique from Anthropic's context
	// engineering research (2025-2026): mechanically replace old re-fetchable
	// tool outputs with placeholders to avoid expensive LLM compaction.
	toolResultClearTrigger = 0.75

	// toolResultClearKeepN is the number of most-recent tool results to keep
	// intact when clearing. Older results get their Output replaced with a
	// short placeholder.
	toolResultClearKeepN = 6
)

// precompactDelayCtx is the delay applied before the background compression
// request fires. Tests override this to zero for fast execution.
var precompactDelay = precompactStartDelay

// isRetryableCompactError returns true for transient network/timeout errors
// that warrant a retry from the precompact background goroutine.
func isRetryableCompactError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false // agent shutting down
	}
	s := strings.ToLower(err.Error())
	for _, keyword := range []string{
		"context deadline exceeded",
		"timeout awaiting response headers",
		"connection reset by peer",
		"unexpected eof",
		"broken pipe",
		"tls handshake timeout",
		"server closed idle connection",
		"temporary failure in name resolution",
	} {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
}

type snapshotCompactManager interface {
	CompactSnapshot() ctxpkg.CompactSnapshot
	ApplyCompactResult(ctxpkg.CompactSnapshot, ctxpkg.CompactResult) (bool, int)
}

// PreCompactStatus is a UI-friendly snapshot of any in-flight pre-compact.
// Returned by Agent.PreCompactStatus(). All fields are zero-valued when none
// is running.
type PreCompactStatus struct {
	Running   bool
	StartedAt time.Time
	StartTok  int
}

// StartPreCompact initiates a background compaction if conditions warrant it.
// Returns immediately. Safe to call after every agent run; it self-skips when
// tokens are below threshold or a compact is already in flight.
//
// The background goroutine compacts an immutable snapshot with its own
// context.WithTimeout(60s). It never mutates the live context directly; the
// result is applied later by consumeReadyPreCompact only if it has already
// finished at a safe run boundary.
func (a *Agent) StartPreCompact() {
	a.mu.Lock()
	if a.precompact != nil {
		a.mu.Unlock()
		debug.Log("precompact", "SKIP: already running")
		return
	}
	cm := a.contextManager
	prov := a.provider
	if cm == nil || prov == nil {
		a.mu.Unlock()
		return
	}
	snapshotMgr, ok := cm.(snapshotCompactManager)
	if !ok {
		a.mu.Unlock()
		debug.Log("precompact", "SKIP: context manager does not support snapshot compaction")
		return
	}
	threshold := cm.AutoCompactThreshold()
	tokens := cm.TokenCount()

	// Proactive tool-result clearing: try cheap mechanical clearing first.
	// When tokens approach the threshold, replace old re-fetchable tool
	// outputs with short placeholders. This may avoid compaction entirely.
	if threshold > 0 && tokens >= int(float64(threshold)*toolResultClearTrigger) {
		if mgr, ok := cm.(*ctxpkg.Manager); ok {
			freed := mgr.ClearOldToolResults(toolResultClearKeepN)
			if freed > 0 {
				tokens = cm.TokenCount() // re-read after clearing
				debug.Log("precompact", "CLEARED: freed %d tokens from old tool results, tokens now %d (threshold=%d)",
					freed, tokens, threshold)
			}
		}
	}

	if threshold <= 0 || tokens < threshold {
		a.mu.Unlock()
		debug.Log("precompact", "SKIP: tokens=%d threshold=%d", tokens, threshold)
		return
	}
	snapshot := snapshotMgr.CompactSnapshot()

	// Use the agent's shutdown context as base so precompact is cancelled
	// when the agent is closed, not just on timeout.
	bgCtx, cancel := context.WithTimeout(a.shutdownCtx, precompactBackgroundTimeout)
	pc := &precompactState{
		done:      make(chan struct{}),
		cancel:    cancel,
		startedAt: time.Now(),
		startTok:  tokens,
		snapshot:  snapshot,
	}
	a.precompact = pc
	a.mu.Unlock()

	debug.Log("precompact", "START: tokens=%d threshold=%d", tokens, threshold)

	safego.Go("agent.precompact.run", func() {
		// Capture pc locally — never touch a.precompact directly from this
		// goroutine, otherwise a concurrent consumeReadyPreCompact +
		// StartPreCompact pair could redirect our writes/close to a NEW state
		// struct and corrupt the channel close invariant.
		defer close(pc.done)
		defer cancel()

		// Delay before sending the compression request.
		// When precompact triggers, the agent's regular LLM turn fires
		// simultaneously — the API rate-limits one of them if both hit at
		// the same time. Staggering them avoids the collision.
		if d := precompactDelay; d > 0 {
			select {
			case <-bgCtx.Done():
				pc.err = bgCtx.Err()
				debug.Log("precompact", "CANCELLED before delay completed")
				return
			case <-time.After(d):
			}
		}

		result, err := snapshot.Compact(bgCtx, prov)
		if err != nil && isRetryableCompactError(err) && a.shutdownCtx.Err() == nil {
			debug.Log("precompact", "first attempt failed (%v), retrying...", err)
			retryCtx, retryCancel := context.WithTimeout(a.shutdownCtx, precompactBackgroundTimeout)
			defer retryCancel()
			result, err = snapshot.Compact(retryCtx, prov)
		}
		if err != nil {
			pc.err = err
			debug.Log("precompact", "FAILED: %v", err)
			return
		}
		pc.result = result
		debug.Log("precompact", "DONE: %d → %d tokens changed=%t (elapsed=%s)",
			tokens, result.TokenCount, result.Changed, time.Since(pc.startedAt).Round(time.Millisecond))
	})
}

// consumeReadyPreCompact applies a completed background pre-compact without
// waiting. If compaction is still running, the current LLM turn proceeds with
// the existing context; a later turn can apply the result.
func (a *Agent) consumeReadyPreCompact(onEvent func(provider.StreamEvent)) bool {
	a.mu.RLock()
	pc := a.precompact
	a.mu.RUnlock()

	if pc == nil {
		return false
	}

	select {
	case <-pc.done:
		// Clear the slot. Safe under lock — the goroutine no longer touches
		// a.precompact (it only touches the captured pc).
		a.mu.Lock()
		if a.precompact == pc {
			a.precompact = nil
		}
		a.mu.Unlock()
		if pc.err != nil || pc.cancelled {
			debug.Log("precompact", "READY but unusable err=%v cancelled=%v", pc.err, pc.cancelled)
			if onEvent != nil {
				if pc.cancelled {
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Auto-compressing context... cancelled]"})
				} else {
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Auto-compressing context... failed: %v]", pc.err)})
				}
			}
			return false
		}
		snapshotMgr, ok := a.contextManager.(snapshotCompactManager)
		if !ok {
			debug.Log("precompact", "READY but context manager cannot apply snapshots")
			return false
		}
		applied, newTokens := snapshotMgr.ApplyCompactResult(pc.snapshot, pc.result)
		debug.Log("precompact", "READY consumed applied=%t tokens=%d startTok=%d result.changed=%t result.msgs=%d", applied, newTokens, pc.startTok, pc.result.Changed, len(pc.result.Messages))
		if !applied {
			reason := "unknown"
			if !pc.result.Changed {
				reason = "summarization produced no change"
			} else if len(pc.result.Messages) == 0 {
				reason = "summarization produced empty result"
			} else {
				reason = "live messages shrunk below snapshot size"
			}
			debug.Log("precompact", "RESULT DISCARDED: %s (snapshot.OrigLen=%d live=%d)", reason, pc.snapshot.OrigLen, len(a.contextManager.Messages()))
			if onEvent != nil {
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Auto-compressing context... result discarded (messages changed)]"})
			}
			return false
		}
		a.maybeSaveCheckpoint()
		if onEvent != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Auto-compressing context... done (%d → %d tokens)] ", pc.startTok, newTokens)})
		}
		return true
	default:
		debug.Log("precompact", "still running; continuing without waiting")
		return false
	}
}

// CancelPreCompact aborts any in-flight pre-compact. Safe to call from
// session-clear, /clear, /compact, SetContextManager or app shutdown.
//
// The goroutine sees its bgCtx cancelled, marks the state as cancelled, then
// closes done. A later consumeReadyPreCompact will discard the result.
func (a *Agent) CancelPreCompact() {
	a.mu.Lock()
	pc := a.precompact
	a.precompact = nil
	a.mu.Unlock()
	if pc == nil {
		return
	}
	pc.cancelled = true
	if pc.cancel != nil {
		pc.cancel()
	}
	debug.Log("precompact", "CANCELLED externally")
}

// PreCompactStatus reports the current background pre-compact status, or a
// zero value if none is running. Used by the TUI status panel to surface a
// "compacting…" indicator instead of leaving the user wondering why tokens are
// at the threshold.
func (a *Agent) PreCompactStatus() PreCompactStatus {
	a.mu.RLock()
	pc := a.precompact
	a.mu.RUnlock()
	if pc == nil {
		return PreCompactStatus{}
	}
	// Drain the done channel non-blockingly to detect completion.
	select {
	case <-pc.done:
		return PreCompactStatus{}
	default:
	}
	return PreCompactStatus{
		Running:   true,
		StartedAt: pc.startedAt,
		StartTok:  pc.startTok,
	}
}
