package agent

// Cost Budget — Session-Level Token Budget Enforcement
//
// While budget_guard.go detects per-iteration COST ESCALATION patterns
// (relative trend analysis, BAGEN-inspired), this module enforces an
// ABSOLUTE session-level token budget. It answers: "how much have we
// spent TOTAL this session, and should we warn or stop?"
//
// Competitor analysis:
//   - Claude Code: shows real-time cost, no hard limit
//   - Cursor: shows cost per request, monthly subscription limits
//   - Cline/OpenHands: shows cost tracking, no session limit
//   - Aider: shows cost, no enforcement
//   - Windsurf: subscription-based, no session limit
//
// Gap: None of these enforce a per-session token budget that proactively
// warns the agent at multiple thresholds and can hard-stop when exceeded.
// Users running autopilot or long-running tasks can burn through tokens
// with no guardrail.
//
// Implementation:
//   - Tracks total input + output tokens consumed per session
//   - Progressive warnings at 75% and 90% of budget
//   - Hard stop at 100% with a clear message
//   - Fires each threshold at most once per run
//   - Budget is configurable via session_token_budget config option (0 = unlimited)

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// costBudgetWarnThreshold: warn the agent when it has consumed this
	// fraction of the session token budget. The message encourages
	// convergence rather than stopping.
	costBudgetWarnThreshold = 0.75

	// costBudgetUrgentThreshold: inject an urgent convergence message
	// when this fraction of the budget is consumed.
	costBudgetUrgentThreshold = 0.90

	// costBudgetStopThreshold: hard stop when this fraction is consumed.
	// 1.0 = stop exactly at the budget. We use a small epsilon tolerance
	// so floating-point accumulation doesn't overshoot.
	costBudgetStopThreshold = 1.0
)

// sessionCostBudget tracks total token consumption against a configurable
// session budget. Unlike budgetGuardState (which tracks per-step trends),
// this tracks absolute cumulative consumption.
type sessionCostBudget struct {
	// totalTokens is the cumulative input+output tokens consumed this session.
	totalTokens int64

	// budget is the maximum tokens allowed (0 = unlimited).
	budget int64

	// warn75Given tracks whether the 75% warning has been injected.
	warn75Given bool

	// warn90Given tracks whether the 90% urgent warning has been injected.
	warn90Given bool

	// stopGiven tracks whether the 100% stop message has been injected.
	stopGiven bool
}

func newSessionCostBudget() *sessionCostBudget {
	return &sessionCostBudget{}
}

// SetBudget sets the token budget. A value of 0 disables budget enforcement.
func (c *sessionCostBudget) SetBudget(budget int64) {
	c.budget = budget
}

// reset clears all accumulated state for a new run.
func (c *sessionCostBudget) reset() {
	c.totalTokens = 0
	c.warn75Given = false
	c.warn90Given = false
	c.stopGiven = false
}

// recordStep adds this iteration's token usage to the cumulative total.
func (c *sessionCostBudget) recordStep(inputTokens, outputTokens int) {
	c.totalTokens += int64(inputTokens) + int64(outputTokens)
}

// check evaluates the budget and returns a message and a stop flag.
// The message is non-empty when a threshold is crossed for the first time.
// stop is true when the budget is fully consumed.
func (c *sessionCostBudget) check() (msg string, stop bool) {
	if c.budget <= 0 {
		return "", false
	}

	pct := float64(c.totalTokens) / float64(c.budget)

	// Hard stop at 100%
	if pct >= costBudgetStopThreshold && !c.stopGiven {
		c.stopGiven = true
		debug.Log("cost-budget", "session budget exhausted: consumed=%d budget=%d (%.0f%%)",
			c.totalTokens, c.budget, pct*100)
		return fmt.Sprintf(
			"[cost budget] Session token budget exhausted (%s / %s tokens, %.0f%%). Stopping to prevent further cost overrun. Summarize what was accomplished and what remains.",
			formatTokenCount(c.totalTokens), formatTokenCount(c.budget), pct*100,
		), true
	}

	// Urgent warning at 90%
	if pct >= costBudgetUrgentThreshold && !c.warn90Given {
		c.warn90Given = true
		debug.Log("cost-budget", "session budget 90%% reached: consumed=%d budget=%d",
			c.totalTokens, c.budget)
		return fmt.Sprintf(
			"[cost budget] Approaching session token budget limit (%s / %s tokens, %.0f%%). Finalize current work, run verification, and prepare a summary. Avoid starting new tasks.",
			formatTokenCount(c.totalTokens), formatTokenCount(c.budget), pct*100,
		), false
	}

	// Early warning at 75%
	if pct >= costBudgetWarnThreshold && !c.warn75Given {
		c.warn75Given = true
		debug.Log("cost-budget", "session budget 75%% reached: consumed=%d budget=%d",
			c.totalTokens, c.budget)
		return fmt.Sprintf(
			"[cost budget] Session token budget 75%% consumed (%s / %s tokens, %.0f%%). Be mindful of remaining budget — prioritize core work and batch operations.",
			formatTokenCount(c.totalTokens), formatTokenCount(c.budget), pct*100,
		), false
	}

	return "", false
}

// formatTokenCount formats a token count for human-readable display.
func formatTokenCount(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}
