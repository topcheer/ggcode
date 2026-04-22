package agent

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// precompactState tracks an in-flight background pre-compaction.
//
// Lifecycle:
//
//	idle ──► StartPreCompact() creates state, launches goroutine ──► COMPACTING
//	COMPACTING ──► goroutine writes err then closes done ──► DONE
//	next RunStreamWithContent ──► waitForPreCompact consumes ──► cleared
//
// All field reads on this struct are guarded by either:
//   - holding Agent.mu (for the *precompactState pointer itself), or
//   - having observed the close of `done` (happens-before via channel close).
type precompactState struct {
	done      chan struct{}      // closed when the goroutine exits
	cancel    context.CancelFunc // cancels this pre-compact's own bgCtx
	startedAt time.Time          // when the goroutine started; for status display
	startTok  int                // token count snapshot when started
	err       error              // populated before close(done) — read only after <-done
	cancelled bool               // set if CancelPreCompact was called externally
}

const precompactBackgroundTimeout = 60 * time.Second

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
// The background goroutine uses its own context.WithTimeout(60s) — independent
// of any user request — so a user pressing ctrl+c during the next prompt does
// NOT abort this work. The result is consumed by waitForPreCompact on the next
// RunStreamWithContent call.
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
	threshold := cm.AutoCompactThreshold()
	tokens := cm.TokenCount()
	if threshold <= 0 || tokens < threshold {
		a.mu.Unlock()
		debug.Log("precompact", "SKIP: tokens=%d threshold=%d", tokens, threshold)
		return
	}

	bgCtx, cancel := context.WithTimeout(context.Background(), precompactBackgroundTimeout)
	pc := &precompactState{
		done:      make(chan struct{}),
		cancel:    cancel,
		startedAt: time.Now(),
		startTok:  tokens,
	}
	a.precompact = pc
	a.mu.Unlock()

	debug.Log("precompact", "START: tokens=%d threshold=%d", tokens, threshold)

	safego.Go("agent.precompact.run", func() {
		// Capture pc locally — never touch a.precompact directly from this
		// goroutine, otherwise a concurrent waitForPreCompact + StartPreCompact
		// pair could redirect our writes/close to a NEW state struct and
		// corrupt the channel close invariant.
		defer close(pc.done)
		defer cancel()

		changed, err := cm.CheckAndSummarize(bgCtx, prov)
		if err != nil {
			pc.err = err
			debug.Log("precompact", "FAILED: %v", err)
			return
		}
		if changed {
			a.maybeSaveCheckpoint()
		}
		newTokens := cm.TokenCount()
		debug.Log("precompact", "DONE: %d → %d tokens (elapsed=%s)",
			tokens, newTokens, time.Since(pc.startedAt).Round(time.Millisecond))
	})
}

// waitForPreCompact waits for an in-flight pre-compact to finish, respecting
// the caller's ctx. Returns true if the pre-compact had a successful, usable
// result; false otherwise (no pre-compact, or it failed/was cancelled).
//
// On ctx cancellation: returns false immediately, but does NOT clear
// a.precompact — the goroutine keeps running on its own bgCtx, and the next
// RunStreamWithContent will pick up the result.
func (a *Agent) waitForPreCompact(ctx context.Context) bool {
	a.mu.RLock()
	pc := a.precompact
	a.mu.RUnlock()

	if pc == nil {
		return false
	}

	debug.Log("precompact", "WAITING for background compaction")
	select {
	case <-pc.done:
		// Clear the slot. Safe under lock — the goroutine no longer touches
		// a.precompact (it only touches the captured pc).
		a.mu.Lock()
		if a.precompact == pc {
			a.precompact = nil
		}
		a.mu.Unlock()
		ok := pc.err == nil && !pc.cancelled
		debug.Log("precompact", "WAIT resolved ok=%v err=%v cancelled=%v", ok, pc.err, pc.cancelled)
		return ok
	case <-ctx.Done():
		// User cancelled their request. Leave a.precompact intact; the
		// goroutine will finish on its own bgCtx and the next call benefits.
		debug.Log("precompact", "WAIT interrupted by ctx: %v", ctx.Err())
		return false
	}
}

// CancelPreCompact aborts any in-flight pre-compact. Safe to call from
// session-clear, /clear, /compact, SetContextManager or app shutdown.
//
// The goroutine sees its bgCtx cancelled, marks the state as cancelled, then
// closes done. Any concurrent waitForPreCompact will see ok=false and fall
// back to inline maybeAutoCompact.
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
