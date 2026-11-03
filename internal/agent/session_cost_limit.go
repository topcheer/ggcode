package agent

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

var agentSessionCostLimits sync.Map

type sessionCostLimitState struct {
	mu                                                   sync.Mutex
	limitUSD                                             float64
	spentUSD                                             float64
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

func (a *Agent) SessionCostLimit() float64 {
	if a == nil {
		return 0
	}
	st := sessionCostLimitStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.limitUSD
}

func (a *Agent) SessionCostSpent() float64 {
	if a == nil {
		return 0
	}
	st := sessionCostLimitStateFor(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.spentUSD
}

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
