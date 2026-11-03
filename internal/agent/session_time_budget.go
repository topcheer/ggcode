package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	sessionTimeBudgetWarnThreshold = 0.80

	sessionTimeBudgetUrgentThreshold = 0.95

	sessionTimeBudgetStopThreshold = 1.0
)

var agentSessionTimeBudgets sync.Map

type sessionTimeBudgetState struct {
	mu       sync.Mutex
	budget   time.Duration
	runStart time.Time

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

func setAgentSessionTimeBudget(a *Agent, budget time.Duration) {
	if a == nil {
		return
	}
	st := sessionTimeBudgetStateFor(a)
	st.mu.Lock()
	st.budget = budget
	st.mu.Unlock()
}

func (a *Agent) SessionTimeBudget() time.Duration {
	if a == nil {
		return 0
	}
	st := sessionTimeBudgetStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.budget
}

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

		st.runStart = time.Now()
		return "", false
	}
	return st.checkLocked(time.Since(st.runStart))
}

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

func (s *sessionTimeBudgetState) checkLocked(elapsed time.Duration) (string, bool) {
	if s.budget <= 0 {
		return "", false
	}
	pct := float64(elapsed) / float64(s.budget)

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
