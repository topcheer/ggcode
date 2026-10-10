package agent

// Session Cost Limit — per-run USD spend ceiling (sa-56 NEW_GAP)
//
// The cost-intelligence design doc (docs/design/cost-intelligence.md:94)
// reserved a budget_guard enforcement point that was never implemented:
// ggcode had full spend *transparency* (/cost, status-bar cost hint,
// per-model breakdown) but no spend *control* — a runaway loop on a
// metered API kept spending until max_iterations with zero guardrails,
// and the $2.00 red tint in view_status.go:474 was purely cosmetic.
//
// This file mirrors session_token_budget.go's storage + enforcement
// pattern (itself modeled on tool_call_budget.go):
//
//   - storage:  SetSessionCostLimit (rates resolved by the agentruntime
//     apply site from the cost pricing table; a zero limit disables)
//   - getter:   Agent.SessionCostLimit()
//   - check:    Agent.RecordSessionCostUsage(...) accumulates USD and
//     returns progressive guidance + a stop flag at 80% / 95% / 100%,
//     identical thresholds to the token budget ladder.
//
// Pricing caveats (documented deliberately):
//   - Only metered models with known rates are priced. Subscription /
//     bundled / free plans and unknown-pricing models accumulate $0 —
//     for those, session_token_budget is the effective ceiling.
//   - Rates are resolved once at apply time; a mid-run model switch
//     re-applies with the new model's rates.
//
// State lives in a package-level sync.Map keyed by *Agent (same #3178
// rationale as the token budget); Agent.Close releases the entry.

import (
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	sessionCostLimitWarnThreshold   = 0.80
	sessionCostLimitUrgentThreshold = 0.95
	sessionCostLimitStopThreshold   = 1.0
)

var agentSessionCostLimits sync.Map // map[*Agent]*sessionCostLimitState // released by Agent.Close

type sessionCostLimitState struct {
	mu       sync.Mutex
	limitUSD float64 // 0 = disabled
	spentUSD float64
	// per-million rates applied to each turn's usage
	inputPerM, outputPerM, cacheReadPerM, cacheWritePerM float64

	warn80Given bool
	warn95Given bool
	stopGiven   bool
}

func sessionCostLimitStateFor(a *Agent) *sessionCostLimitState {
	if v, ok := agentSessionCostLimits.Load(a); ok {
		return v.(*sessionCostLimitState)
	}
	v, _ := agentSessionCostLimits.LoadOrStore(a, &sessionCostLimitState{})
	return v.(*sessionCostLimitState)
}

// SetSessionCostLimit configures the per-run USD ceiling and the
// per-million-token rates used to price each turn. A limit <= 0 disables
// enforcement (rates are still stored; the runtime apply site always
// calls this so a config reload that removes the key resets state).
func (a *Agent) SetSessionCostLimit(limitUSD, inputPerM, outputPerM, cacheReadPerM, cacheWritePerM float64) {
	if a == nil {
		return
	}
	st := sessionCostLimitStateFor(a)
	st.mu.Lock()
	st.limitUSD = limitUSD
	st.inputPerM = inputPerM
	st.outputPerM = outputPerM
	st.cacheReadPerM = cacheReadPerM
	st.cacheWritePerM = cacheWritePerM
	st.mu.Unlock()
}

// SessionCostLimit returns the configured USD ceiling (0 = disabled).
func (a *Agent) SessionCostLimit() float64 {
	if a == nil {
		return 0
	}
	st := sessionCostLimitStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.limitUSD
}

// SessionCostSpent returns the accumulated USD spend for this run
// (priced turns only; see file comment on unpriced models).
func (a *Agent) SessionCostSpent() float64 {
	if a == nil {
		return 0
	}
	st := sessionCostLimitStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.spentUSD
}

// RecordSessionCostUsage prices one LLM turn's token usage and evaluates
// the USD budget, mirroring RecordSessionTokenUsage's threshold ladder.
// Returns guidance on first threshold crossings and stop=true at 100%.
func (a *Agent) RecordSessionCostUsage(inputTokens, outputTokens, cacheRead, cacheWrite int) (string, bool) {
	if a == nil {
		return "", false
	}
	st := sessionCostLimitStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	turn := float64(inputTokens)*st.inputPerM/1e6 +
		float64(outputTokens)*st.outputPerM/1e6 +
		float64(cacheRead)*st.cacheReadPerM/1e6 +
		float64(cacheWrite)*st.cacheWritePerM/1e6
	if turn <= 0 {
		return "", false
	}
	st.spentUSD += turn
	if st.limitUSD <= 0 {
		return "", false
	}
	pct := st.spentUSD / st.limitUSD

	if pct >= sessionCostLimitUrgentThreshold && !st.warn95Given {
		st.warn95Given = true
		st.warn80Given = true
		debug.Log("session-cost-limit", "95%% reached: $%.2f / $%.2f", st.spentUSD, st.limitUSD)
		return fmt.Sprintf(
			"[cost limit] Session spend limit 95%% consumed ($%.2f of $%.2f). "+
				"Finalize current work and prepare to conclude.",
			st.spentUSD, st.limitUSD), false
	}
	if pct >= sessionCostLimitStopThreshold && !st.stopGiven {
		st.stopGiven = true
		debug.Log("session-cost-limit", "exhausted: $%.2f / $%.2f", st.spentUSD, st.limitUSD)
		return fmt.Sprintf(
			"[cost limit] Session spend limit exhausted ($%.2f of $%.2f). "+
				"Stopping to prevent further spend. Summarize what was accomplished and what remains.",
			st.spentUSD, st.limitUSD), true
	}
	if pct >= sessionCostLimitWarnThreshold && !st.warn80Given {
		st.warn80Given = true
		debug.Log("session-cost-limit", "80%% reached: $%.2f / $%.2f", st.spentUSD, st.limitUSD)
		return fmt.Sprintf(
			"[cost limit] Session spend limit 80%% consumed ($%.2f of $%.2f). "+
				"Prioritize remaining work — batch operations and avoid redundant reads to reduce spend.",
			st.spentUSD, st.limitUSD), false
	}
	return "", false
}

// resetSessionCostUsage clears per-run accumulation (limit and rates kept).
func (a *Agent) resetSessionCostUsage() {
	if a == nil {
		return
	}
	st := sessionCostLimitStateFor(a)
	st.mu.Lock()
	st.spentUSD = 0
	st.warn80Given = false
	st.warn95Given = false
	st.stopGiven = false
	st.mu.Unlock()
}
