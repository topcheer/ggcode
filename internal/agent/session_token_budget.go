package agent

// Session Token Budget — per-run total token ceiling (issue #543)
//
// SetSessionTokenBudget in agent.go previously had an empty body: the
// session_token_budget config option (documented in ggcode.example.yaml)
// was parsed, validated, and shown by `config list` — but never stored,
// never read, never enforced. End-to-end no-op.
//
// This file provides storage and the enforcement primitive, mirroring
// tool_call_budget.go's storage + consumption pattern:
//
//   - storage:  SetSessionTokenBudget (agent.go) -> setAgentSessionTokenBudget
//   - getter:   Agent.SessionTokenBudget() — runtime/UI observe the cap
//   - check:    Agent.RecordSessionTokenUsage(input, output) accumulates
//     usage and returns progressive guidance + a stop flag, ready to call
//     from the agent loop's TokenUsage accumulation site (progressive 80% /
//     95% warnings and a 100% hard stop, identical thresholds to
//     toolCallBudget.check()).
//
// State lives in a package-level sync.Map keyed by *Agent because issue
// #543's scope confines agent.go edits to the SetSessionTokenBudget
// function body (no new struct field there). Entries live for the process
// lifetime of their agent — bounded by the number of agent instances per
// process, and each Agent is long-lived.

import (
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// sessionTokenBudgetWarnThreshold: inject convergence guidance when this
	// fraction of the token budget is consumed.
	sessionTokenBudgetWarnThreshold = 0.80

	// sessionTokenBudgetUrgentThreshold: inject urgent finalize pressure.
	sessionTokenBudgetUrgentThreshold = 0.95

	// sessionTokenBudgetStopThreshold: hard stop when consumed.
	sessionTokenBudgetStopThreshold = 1.0
)

// agentSessionTokenBudgets stores per-agent session token budget state,
// keyed by agent pointer. See the file comment for why this is not an
// Agent struct field.
var agentSessionTokenBudgets sync.Map // map[*Agent]*sessionTokenBudgetState

// sessionTokenBudgetState tracks cumulative token usage against a
// configurable per-run budget (input + output tokens).
type sessionTokenBudgetState struct {
	mu     sync.Mutex
	budget int64 // maximum total tokens; 0 = no enforcement
	used   int64 // cumulative input+output tokens this run

	// Threshold flags so each fires at most once per run.
	warn80Given bool
	warn95Given bool
	stopGiven   bool
}

func newSessionTokenBudgetState() *sessionTokenBudgetState {
	return &sessionTokenBudgetState{}
}

// sessionTokenBudgetStateFor returns (creating on demand) the budget state
// for an agent.
func sessionTokenBudgetStateFor(a *Agent) *sessionTokenBudgetState {
	if v, ok := agentSessionTokenBudgets.Load(a); ok {
		return v.(*sessionTokenBudgetState)
	}
	v, _ := agentSessionTokenBudgets.LoadOrStore(a, newSessionTokenBudgetState())
	return v.(*sessionTokenBudgetState)
}

// setAgentSessionTokenBudget stores the configured budget for an agent.
// A value of 0 disables enforcement and clears any previously set budget.
func setAgentSessionTokenBudget(a *Agent, budget int64) {
	if a == nil {
		return
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	st.budget = budget
	st.mu.Unlock()
}

// SessionTokenBudget returns the configured per-run token budget
// (0 = not configured / unlimited). This is the getter the runtime and UI
// use to observe the cap (#543).
func (a *Agent) SessionTokenBudget() int64 {
	if a == nil {
		return 0
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.budget
}

// ToolCallBudget returns the explicitly configured per-run tool call budget
// (0 = none configured; auto-derivation from maxIter may still apply).
// Exposed so the agentruntime hot-reload path can verify that removing
// tool_call_budget from the config actually resets the previously applied
// value (#543 sister bug).
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

// RecordSessionTokenUsage accumulates input+output tokens for this agent's
// current run and evaluates the budget, mirroring toolCallBudget.check().
// Returns a guidance message when a threshold is crossed for the first
// time, and stop=true when the budget is fully consumed. This is the
// consumption primitive for the agent loop's usage-accumulation site.
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

// resetSessionTokenUsage clears per-run accumulation (budget config is
// kept). Wire-ready for the run-start reset site alongside
// toolCallBudget.reset().
func (a *Agent) resetSessionTokenUsage() {
	if a == nil {
		return
	}
	st := sessionTokenBudgetStateFor(a)
	st.mu.Lock()
	st.used = 0
	st.warn80Given = false
	st.warn95Given = false
	st.stopGiven = false
	st.mu.Unlock()
}

// checkLocked evaluates thresholds; the caller holds st.mu.
func (s *sessionTokenBudgetState) checkLocked() (string, bool) {
	if s.budget <= 0 {
		return "", false
	}
	pct := float64(s.used) / float64(s.budget)

	// Urgent 95% warning is evaluated BEFORE the hard stop so the finalize
	// guidance always fires once, even when a single call jumps straight past
	// 100%; the next crossing then hard-stops (#543).
	if pct >= sessionTokenBudgetUrgentThreshold && !s.warn95Given {
		s.warn95Given = true
		s.warn80Given = true
		debug.Log("session-token-budget", "95%% reached: used=%d budget=%d", s.used, s.budget)
		return fmt.Sprintf(
			"[token budget] Session token budget 95%% consumed (%d / %d tokens, %.0f%%). "+
				"Finalize current work, run any remaining verification, and prepare to conclude.",
			s.used, s.budget, pct*100), false
	}

	// Hard stop at 100%
	if pct >= sessionTokenBudgetStopThreshold && !s.stopGiven {
		s.stopGiven = true
		debug.Log("session-token-budget", "budget exhausted: used=%d budget=%d (%.0f%%)",
			s.used, s.budget, pct*100)
		return fmt.Sprintf(
			"[token budget] Session token budget exhausted (%d / %d tokens, %.0f%%). "+
				"Stopping to prevent further spend. Summarize what was accomplished and what remains.",
			s.used, s.budget, pct*100), true
	}

	// Early warning at 80%
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
