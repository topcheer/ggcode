package agent

import (
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	sessionTokenBudgetWarnThreshold = 0.80

	sessionTokenBudgetUrgentThreshold = 0.95

	sessionTokenBudgetStopThreshold = 1.0
)

var agentSessionTokenBudgets sync.Map

type sessionTokenBudgetState struct {
	mu     sync.Mutex
	budget int64
	used   int64

	delegatedUsed int64

	pendingMsg  string
	pendingStop bool

	warn80Given bool
	warn95Given bool
	stopGiven   bool
}

func newSessionTokenBudgetState() *sessionTokenBudgetState {
	return &sessionTokenBudgetState{}
}

func sessionTokenBudgetStateFor(a *Agent) *sessionTokenBudgetState {
	if v, ok := agentSessionTokenBudgets.Load(a); ok {
		return v.(*sessionTokenBudgetState)
	}
	v, _ := agentSessionTokenBudgets.LoadOrStore(a, newSessionTokenBudgetState())
	return v.(*sessionTokenBudgetState)
}

func setAgentSessionTokenBudget(a *Agent, budget int64) {
	if a == nil {
		return
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	st.budget = budget
	st.mu.Unlock()
}

func (a *Agent) SessionTokenBudget() int64 {
	if a == nil {
		return 0
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.budget
}

func (a *Agent) ToolCallBudget() int {
	if a == nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.toolCallBudget == nil {
		return 0
	}
	return a.toolCallBudget.budget
}

func (a *Agent) RecordSessionTokenUsage(inputTokens, outputTokens int64) (string, bool) {
	if a == nil || (inputTokens <= 0 && outputTokens <= 0) {
		return "", false
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.used += inputTokens + outputTokens
	return st.checkLocked()
}

func (a *Agent) RecordDelegatedTokenUsage(inputTokens, outputTokens int64) {
	if a == nil || (inputTokens <= 0 && outputTokens <= 0) {
		return
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.used += inputTokens + outputTokens
	st.delegatedUsed += inputTokens + outputTokens
	if msg, stop := st.checkLocked(); msg != "" {
		st.pendingMsg = msg
		st.pendingStop = st.pendingStop || stop
		debug.Log("session-token-budget", "delegated threshold crossed: used=%d delegated=%d budget=%d",
			st.used, st.delegatedUsed, st.budget)
	}
}

func (a *Agent) SessionTokenBudgetAlert() (string, bool) {
	if a == nil {
		return "", false
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	msg, stop := st.pendingMsg, st.pendingStop
	st.pendingMsg, st.pendingStop = "", false
	return msg, stop
}

func (a *Agent) DelegatedTokenUsage() int64 {
	if a == nil {
		return 0
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.delegatedUsed
}

func (a *Agent) resetSessionTokenUsage() {
	if a == nil {
		return
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	st.used = 0
	st.delegatedUsed = 0
	st.pendingMsg = ""
	st.pendingStop = false
	st.warn80Given = false
	st.warn95Given = false
	st.stopGiven = false
	st.mu.Unlock()
}

func (s *sessionTokenBudgetState) checkLocked() (string, bool) {
	if s.budget <= 0 {
		return "", false
	}
	pct := float64(s.used) / float64(s.budget)

	if pct >= sessionTokenBudgetUrgentThreshold && !s.warn95Given {
		s.warn95Given = true
		s.warn80Given = true
		debug.Log("session-token-budget", "95%% reached: used=%d budget=%d", s.used, s.budget)
		return fmt.Sprintf(
			"[token budget] Session token budget 95%% consumed (%d / %d tokens, %.0f%%). "+
				"Finalize current work, run any remaining verification, and prepare to conclude.",
			s.used, s.budget, pct*100), false
	}

	if pct >= sessionTokenBudgetStopThreshold && !s.stopGiven {
		s.stopGiven = true
		debug.Log("session-token-budget", "budget exhausted: used=%d budget=%d (%.0f%%)",
			s.used, s.budget, pct*100)
		return fmt.Sprintf(
			"[token budget] Session token budget exhausted (%d / %d tokens, %.0f%%). "+
				"Stopping to prevent further spend. Summarize what was accomplished and what remains.",
			s.used, s.budget, pct*100), true
	}

	if pct >= sessionTokenBudgetWarnThreshold && !s.warn80Given {
		s.warn80Given = true
		debug.Log("session-token-budget", "80%% reached: used=%d budget=%d", s.used, s.budget)
		return fmt.Sprintf(
			"[token budget] Session token budget 80%% consumed (%d / %d tokens, %.0f%%). "+
				"Prioritize remaining work — batch operations and avoid redundant reads to reduce token spend.",
			s.used, s.budget, pct*100), false
	}

	return "", false
}
