package agent

import (
	"github.com/topcheer/ggcode/internal/provider"
)

// #5543: loop-core extraction. The post-chat-turn usage/budget recording
// block (session token, cost, sub-agent-inclusive alert, wall-clock time)
// becomes an explicit method; behavior unchanged (verbatim body).

// recordUsageBudgets records the four session usage dimensions and reports
// whether any budget is now exhausted (sessionBudgetStop semantics: the
// first crossing wins, later crossings keep it armed).
func (a *Agent) recordUsageBudgets(i int, resp *provider.ChatResponse, onEvent func(provider.StreamEvent)) bool {
	sessionBudgetStop := false
	if msg, stop := a.RecordSessionTokenUsage(int64(resp.Usage.InputTokens), int64(resp.Usage.OutputTokens)); msg != "" {
		if a.noteUsageBudgetCrossing(i+1, msg, stop, "Session Token Budget", "session-token-budget",
			"[Session token budget fully consumed - winding down. Summarize the state so the user can resume with a fresh budget.] ", onEvent) {
			sessionBudgetStop = true
		}
	}

	if msg, stop := a.RecordSessionCostUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheRead, resp.Usage.CacheWrite); msg != "" {
		if a.noteUsageBudgetCrossing(i+1, msg, stop, "Session Cost Limit", "session-cost-limit",
			"[Session spend limit fully consumed - winding down. Summarize the state so the user can resume with a fresh budget.] ", onEvent) {
			sessionBudgetStop = true
		}
	}

	if msg, stop := a.SessionTokenBudgetAlert(); msg != "" {
		if a.noteUsageBudgetCrossing(i+1, msg, stop, "Session Token Budget", "session-token-budget",
			"[Session token budget fully consumed (including sub-agent spend) - winding down. Summarize the state so the user can resume with a fresh budget.] ", onEvent) {
			sessionBudgetStop = true
		}
	}

	if msg, stop := a.RecordSessionTimeUsage(); msg != "" {
		if a.noteUsageBudgetCrossing(i+1, msg, stop, "Session Time Budget", "session-time-budget",
			"[Session time budget fully elapsed - winding down. Summarize the state so the user can resume with a fresh budget.] ", onEvent) {
			sessionBudgetStop = true
		}
	}

	return sessionBudgetStop
}
