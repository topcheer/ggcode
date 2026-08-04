package agent

// Cost Budget — Session-Level Token Tracking
//
// Tracks total input + output tokens consumed per session.
// Budget is configurable via session_token_budget config option (0 = unlimited).
//
// NOTE: This module only tracks token consumption for stats/reporting.
// It does NOT inject any warnings or stop the agent.
// Per project policy, no context/budget warnings may be injected into LLM context.

// sessionCostBudget tracks total token consumption for the session.
type sessionCostBudget struct {
	totalTokens int64 // cumulative input+output tokens consumed
	budget      int64 // configured budget (0 = unlimited), tracked for stats only
}

func newSessionCostBudget() *sessionCostBudget {
	return &sessionCostBudget{}
}

// SetBudget sets the token budget for tracking purposes.
func (c *sessionCostBudget) SetBudget(budget int64) {
	c.budget = budget
}

// reset clears accumulated state for a new run.
func (c *sessionCostBudget) reset() {
	c.totalTokens = 0
}

// recordStep adds this iteration's token usage to the cumulative total.
func (c *sessionCostBudget) recordStep(inputTokens, outputTokens int) {
	c.totalTokens += int64(inputTokens) + int64(outputTokens)
}
