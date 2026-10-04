package swarm

import (
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// r455: teammate-level failure breaker (failure-storm bulkhead).
//
// Frontier basis: multi-agent systems fail in storms (cascade failures) —
// one failing teammate keeps claiming board tasks, failing them, and
// starving the team while healthy teammates idle. The MCP domain already
// proved the breaker pattern (internal/mcp/breaker.go, per-server); this
// is the same three-state circuit keyed by teammate ID, gating board
// CLAIMS (not inbox messages — direct task delivery is the parent's
// explicit routing decision and stays untouched).
//
// Semantics (mirrors internal/mcp/breaker.go):
//   - CLOSED: normal operation. Consecutive failures >= threshold → OPEN.
//   - OPEN: the teammate is skipped by tryClaimPendingTask for cooldown.
//   - HALF-OPEN: exactly one claim is allowed as a probe; success →
//     CLOSED, failure → OPEN again.
//
// Cancellation is not a failure: a teammate aborted by shutdown/team
// teardown must not trip its breaker.

const (
	// teammateBreakerThreshold: consecutive failed tasks before the
	// circuit opens. 3 mirrors the MCP breaker: single transient task
	// failures (flaky test, bad prompt) should not quarantine a teammate.
	teammateBreakerThreshold = 3
)

// teammateBreakerCooldown is how long an OPEN circuit waits before the
// half-open probe. Var so tests can shrink it (fileLockTimeout
// precedent, #1834 case 2).
var teammateBreakerCooldown = 60 * time.Second

type teammateBreakerState int

const (
	teammateBreakerClosed teammateBreakerState = iota
	teammateBreakerOpen
	teammateBreakerHalfOpen
)

// teammateBreaker is the per-teammate circuit. All methods are
// goroutine-safe (claims and results arrive from the runner goroutine,
// status inspection may happen from manager paths).
type teammateBreaker struct {
	mu       sync.Mutex
	state    teammateBreakerState
	failures int       // consecutive failures (CLOSED state)
	openedAt time.Time // when the circuit last opened
}

// tripped reports whether the teammate should be barred from claiming
// board tasks right now. The CLOSED→HALF-OPEN transition itself releases
// the single probe claim; while HALF-OPEN every further claim is barred
// until the probe's result arrives (record closes or re-opens).
// #3245: cooldown expiry only ARMS the probe allowance - it does NOT
// consume it. The slot is spent exclusively by consumeProbe() after a
// REAL board claim succeeds. A gate pass that strands (empty board, lost
// claim race, no task manager) leaves the circuit OPEN+expired, so the
// next tick re-arms: a polled-but-empty board can no longer strand the
// teammate in HALF_OPEN forever (record is HALF_OPEN's only exit).
func (b *teammateBreaker) tripped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case teammateBreakerClosed:
		return false
	case teammateBreakerOpen:
		return time.Since(b.openedAt) < teammateBreakerCooldown
	default: // teammateBreakerHalfOpen: a real probe is in flight
		// #3245 defensive: a probe whose result never arrives (agent
		// crash mid-task, lost result path) must not strand HALF_OPEN
		// either - after a second full cooldown without a verdict, fall
		// back to OPEN with a fresh timer (the next expiry re-arms another
		// probe). At most the ALLOWANCE expires; no second task runs.
		if time.Since(b.openedAt) >= 2*teammateBreakerCooldown {
			b.state = teammateBreakerOpen
			b.openedAt = time.Now()
		}
		return true
	}
}

// consumeProbe transitions an OPEN+expired circuit into HALF_OPEN. It is
// called ONLY after a real board claim succeeded (#3245): the probe slot
// is spent on an actual task, whose result closes or re-opens the circuit
// via record(). Returns true when the slot was actually spent.
func (b *teammateBreaker) consumeProbe() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != teammateBreakerOpen {
		return false
	}
	if time.Since(b.openedAt) < teammateBreakerCooldown {
		return false // cooldown still running: no allowance armed
	}
	b.state = teammateBreakerHalfOpen
	b.openedAt = time.Now() // marks probe start (tripped's fallback window)
	return true
}

// record accounts a task outcome. Success closes the circuit from any
// state; failure increments (or re-opens in HALF-OPEN).
func (b *teammateBreaker) record(failed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case teammateBreakerHalfOpen:
		if failed {
			b.state = teammateBreakerOpen
			b.openedAt = time.Now()
		} else {
			b.state = teammateBreakerClosed
			b.failures = 0
		}
	case teammateBreakerOpen:
		// Result of a straggler task that started before the circuit
		// opened: only a success can close an OPEN circuit early.
		if !failed {
			b.state = teammateBreakerClosed
			b.failures = 0
		}
	default: // CLOSED
		if failed {
			b.failures++
			if b.failures >= teammateBreakerThreshold {
				b.state = teammateBreakerOpen
				b.openedAt = time.Now()
			}
		} else {
			b.failures = 0
		}
	}
}

// stateSnapshot exposes the state for tests/diagnostics.
func (b *teammateBreaker) stateSnapshot() teammateBreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// teammateBreakers is the process-wide registry keyed by teammate ID.
var teammateBreakers sync.Map // string -> *teammateBreaker

// teammateBreakerFor returns (creating if needed) the breaker for a
// teammate.
func teammateBreakerFor(id string) *teammateBreaker {
	if id == "" {
		return nil
	}
	if v, ok := teammateBreakers.Load(id); ok {
		return v.(*teammateBreaker)
	}
	b := &teammateBreaker{}
	actual, _ := teammateBreakers.LoadOrStore(id, b)
	return actual.(*teammateBreaker)
}

// teammateClaimAllowed is the claim gate: false = this teammate's circuit
// is OPEN and it must not claim board tasks. An expired OPEN merely ARMS
// the single probe allowance; the slot is consumed only after a real
// claim succeeds via teammateConsumeProbe (#3245).
func teammateClaimAllowed(teammateID string) bool {
	b := teammateBreakerFor(teammateID)
	if b == nil {
		return true
	}
	allowed := !b.tripped()
	if !allowed {
		debug.Log("swarm", "[teammate-breaker] %s barred from claiming (circuit open)", teammateID)
	}
	return allowed
}

// teammateConsumeProbe spends the armed probe slot after a successful
// board claim (#3245). No-op unless the circuit is OPEN and the cooldown
// has expired - only then does this claim become THE probe task.
func teammateConsumeProbe(teammateID string) {
	b := teammateBreakerFor(teammateID)
	if b == nil {
		return
	}
	if b.consumeProbe() {
		debug.Log("swarm", "[teammate-breaker] %s probe task claimed (half-open)", teammateID)
	}
}

// recordTeammateTaskResult accounts a completed task run. cancelled
// results (team teardown, shutdown) do NOT count as failures.
func recordTeammateTaskResult(teammateID string, failed bool) {
	b := teammateBreakerFor(teammateID)
	if b == nil {
		return
	}
	b.record(failed)
	if failed && b.stateSnapshot() == teammateBreakerOpen {
		debug.Log("swarm", "[teammate-breaker] %s circuit OPEN after consecutive failures", teammateID)
	}
}

// resetTeammateBreakers clears all circuits (tests only).
func resetTeammateBreakers() {
	teammateBreakers.Range(func(k, _ any) bool {
		teammateBreakers.Delete(k)
		return true
	})
}
