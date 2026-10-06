package agent

// Session Time Budget — per-run wall-clock soft-convergence ladder (r415).
//
// The token dimension (session_token_budget.go) already implements the
// BAGEN-style mid-run budget awareness: 80% convergence guidance, 95%
// finalize pressure, 100% wind-down stop. The time dimension had only the
// hard SessionTimeout (ctx cancellation) — no progressive signal the LLM
// can perceive and react to. This file mirrors the token state machine for
// wall-clock time:
//
//   - storage:  SetSessionTimeBudget (agent.go) -> setAgentSessionTimeBudget
//   - getter:   Agent.SessionTimeBudget()
//   - check:    Agent.RecordSessionTimeUsage() evaluates elapsed-vs-budget
//     after each LLM call, from the run-start timestamp set by
//     resetSessionTimeUsage() (called at the same site as
//     resetSessionTokenUsage).
//
// Distinct from SessionTimeout: the timeout is a hard ctx deadline (kill);
// this budget is a soft ladder (steer). A run may legitimately configure
// both — budget 10m steers at 8m/9.5m, timeout 30m kills at 30m.
//
// State lives in a package-level sync.Map keyed by *Agent, mirroring the
// token budget's storage pattern (issue #543 confined agent.go edits to
// setter bodies; the pattern stuck).

import (
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// sessionTimeBudgetWarnThreshold: inject convergence guidance when this
	// fraction of the time budget has elapsed.
	sessionTimeBudgetWarnThreshold = 0.80

	// sessionTimeBudgetUrgentThreshold: inject urgent finalize pressure.
	sessionTimeBudgetUrgentThreshold = 0.95

	// sessionTimeBudgetStopThreshold: wind down when fully elapsed. Not a
	// kill — the run is told to conclude and summarize (an in-flight LLM
	// call cannot be cheaply aborted without losing its output).
	sessionTimeBudgetStopThreshold = 1.0
)

var agentSessionTimeBudgets sync.Map // map[*Agent]*sessionTimeBudgetState

// sessionTimeBudgetState tracks wall-clock elapsed time against a
// configurable per-run budget.
type sessionTimeBudgetState struct {
	mu       sync.Mutex
	budget   time.Duration // 0 = no enforcement
	runStart time.Time     // zero until the first reset/record of a run

	// Threshold flags so each fires at most once per run.
	warn80Given bool
	warn95Given bool
	stopGiven   bool
}

func newSessionTimeBudgetState() *sessionTimeBudgetState {
	return &sessionTimeBudgetState{}
}

func sessionTimeBudgetStateFor(a *Agent) *sessionTimeBudgetState {
	if v, ok := agentSessionTimeBudgets.Load(a); ok {
		return v.(*sessionTimeBudgetState)
	}
	v, _ := agentSessionTimeBudgets.LoadOrStore(a, newSessionTimeBudgetState())
	return v.(*sessionTimeBudgetState)
}

// setAgentSessionTimeBudget stores the configured budget. 0 disables
// enforcement and clears any previously set budget.
func setAgentSessionTimeBudget(a *Agent, budget time.Duration) {
	if a == nil {
		return
	}
	st := sessionTimeBudgetStateFor(a)
	st.mu.Lock()
	st.budget = budget
	st.mu.Unlock()
}

// SessionTimeBudget returns the configured per-run wall-clock budget
// (0 = not configured / unlimited).
func (a *Agent) SessionTimeBudget() time.Duration {
	if a == nil {
		return 0
	}
	st := sessionTimeBudgetStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.budget
}

// RecordSessionTimeUsage evaluates elapsed wall-clock time against the
// budget. Returns a guidance message when a threshold is crossed for the
// first time, and stop=true when the budget is fully elapsed. Call after
// each LLM response, next to RecordSessionTokenUsage.
func (a *Agent) RecordSessionTimeUsage() (string, bool) {
	if a == nil {
		return "", false
	}
	st := sessionTimeBudgetStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.budget <= 0 {
		return "", false
	}
	if st.runStart.IsZero() {
		// Defensive: a consumer that never got the run-start reset still
		// gets a first observation now rather than an instant 100%.
		st.runStart = time.Now()
		return "", false
	}
	return st.checkLocked(time.Since(st.runStart))
}

// resetSessionTimeUsage clears per-run elapsed state (budget config kept)
// and re-arms the run-start timestamp. Called at the same site as
// resetSessionTokenUsage.
func (a *Agent) resetSessionTimeUsage() {
	if a == nil {
		return
	}
	st := sessionTimeBudgetStateFor(a)
	st.mu.Lock()
	st.runStart = time.Now()
	st.warn80Given = false
	st.warn95Given = false
	st.stopGiven = false
	st.mu.Unlock()
}

// checkLocked evaluates thresholds; the caller holds st.mu.
func (s *sessionTimeBudgetState) checkLocked(elapsed time.Duration) (string, bool) {
	if s.budget <= 0 {
		return "", false
	}
	pct := float64(elapsed) / float64(s.budget)

	// Urgent 95% evaluated before the stop so the finalize guidance always
	// fires once even when a long call jumps straight past 100% (mirrors
	// the token ladder's ordering rationale, #543).
	if pct >= sessionTimeBudgetUrgentThreshold && !s.warn95Given {
		s.warn95Given = true
		s.warn80Given = true
		debug.Log("session-time-budget", "95%% reached: elapsed=%s budget=%s", elapsed, s.budget)
		return fmt.Sprintf(
			"[time budget] Session time budget 95%% elapsed (%s / %s). "+
				"Finalize current work, run any remaining verification, and prepare to conclude.",
			elapsed.Round(time.Second), s.budget), false
	}

	if pct >= sessionTimeBudgetStopThreshold && !s.stopGiven {
		s.stopGiven = true
		debug.Log("session-time-budget", "budget exhausted: elapsed=%s budget=%s", elapsed, s.budget)
		return fmt.Sprintf(
			"[time budget] Session time budget exhausted (%s / %s). "+
				"Stop starting new work. Summarize what was accomplished and what remains so the user can resume with a fresh budget.",
			elapsed.Round(time.Second), s.budget), true
	}

	if pct >= sessionTimeBudgetWarnThreshold && !s.warn80Given {
		s.warn80Given = true
		debug.Log("session-time-budget", "80%% reached: elapsed=%s budget=%s", elapsed, s.budget)
		return fmt.Sprintf(
			"[time budget] Session time budget 80%% elapsed (%s / %s). "+
				"Prioritize remaining work — batch operations and avoid long exploratory detours.",
			elapsed.Round(time.Second), s.budget), false
	}

	return "", false
}
