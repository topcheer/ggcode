package agent

// Tool Call Budget — Per-Session Tool Invocation Limit
//
// While cost_budget.go enforces an absolute TOKEN budget and the iteration
// limit (maxIter) caps LLM turns, neither caps the total number of TOOL
// CALLS per session. Each LLM turn can emit multiple tool calls, so an agent
// with maxIter=50 could make 150-250 tool calls. For autopilot (maxIter=0,
// unlimited) there is NO cap at all — the only protection is pattern-based
// detection (overseer, loop_detect) which may not fire quickly enough.
//
// Competitor analysis:
//   - OpenHands: counts each ACTION (tool call) as an iteration — effectively
//     a per-action limit equal to max_iterations
//   - Devin: has an explicit "step budget" that counts tool executions
//   - Claude Code: relies on iteration limit only (no tool call cap)
//   - Cursor: request-based limits (subscription tier)
//   - Aider: no tool call limit
//
// Gap: ggcode counts LLM turns, not tool calls. A runaway agent making many
// small tool calls per turn (common with lower-tier models) can execute far
// more operations than the iteration limit implies. This is especially
// dangerous in autopilot/cron mode where maxIter=0.
//
// Implementation:
//   - Counts total tool calls per run (including pre-executed read-only tools)
//   - Progressive warnings at 80% and 95% of budget
//   - Hard stop at 100% with a clear convergence message
//   - Each threshold fires at most once per run
//   - Budget is configurable via tool_call_budget config option (0 = unlimited)
//   - Default: derived from maxIter (maxIter * 8) when not explicitly set,
//     or 500 when maxIter is unlimited

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// toolCallBudgetWarnThreshold: warn when this fraction of the tool call
	// budget is consumed. Encourages convergence.
	toolCallBudgetWarnThreshold = 0.80

	// toolCallBudgetUrgentThreshold: inject urgent convergence pressure.
	toolCallBudgetUrgentThreshold = 0.95

	// toolCallBudgetStopThreshold: hard stop when consumed.
	toolCallBudgetStopThreshold = 1.0

	// defaultToolCallBudgetUnlimited is the fallback budget when maxIter is 0
	// (unlimited iterations). Provides a safety net for autopilot/cron runs.
	defaultToolCallBudgetUnlimited = 500

	// defaultToolCallBudgetMultiplier is the factor applied to maxIter when
	// deriving a default budget. Assumes ~8 tool calls per LLM turn.
	defaultToolCallBudgetMultiplier = 8
)

// toolCallBudget tracks total tool invocations against a configurable budget.
type toolCallBudget struct {
	// totalCalls is the cumulative tool call count for this run.
	totalCalls int

	// budget is the maximum tool calls allowed (0 = unlimited/no enforcement).
	budget int

	// defaultBudget is the auto-derived budget used when budget==0 and
	// auto-derive is enabled.
	defaultBudget int

	// Threshold flags to ensure each fires at most once.
	warn80Given bool
	warn95Given bool
	stopGiven   bool
}

func newToolCallBudget() *toolCallBudget {
	return &toolCallBudget{}
}

// SetBudget sets an explicit tool call budget. A value of 0 disables explicit
// enforcement (auto-derivation may still apply).
func (t *toolCallBudget) SetBudget(budget int) {
	t.budget = budget
}

// SetDefaultBudget sets the auto-derived budget used when no explicit budget
// is configured. Called after maxIter is known.
func (t *toolCallBudget) SetDefaultBudget(budget int) {
	t.defaultBudget = budget
}

// effectiveBudget returns the budget that applies: explicit if set, else default.
// Returns 0 if neither is set (no enforcement).
func (t *toolCallBudget) effectiveBudget() int {
	if t.budget > 0 {
		return t.budget
	}
	return t.defaultBudget
}

// reset clears all accumulated state for a new run.
func (t *toolCallBudget) reset() {
	t.totalCalls = 0
	t.warn80Given = false
	t.warn95Given = false
	t.stopGiven = false
}

// record increments the tool call counter.
func (t *toolCallBudget) record() {
	t.totalCalls++
}

// check evaluates the budget and returns a guidance message and a stop flag.
// The message is non-empty when a threshold is crossed for the first time.
// stop is true when the budget is fully consumed.
func (t *toolCallBudget) check() (msg string, stop bool) {
	budget := t.effectiveBudget()
	if budget <= 0 {
		return "", false
	}

	pct := float64(t.totalCalls) / float64(budget)

	// Hard stop at 100%
	if pct >= toolCallBudgetStopThreshold && !t.stopGiven {
		t.stopGiven = true
		debug.Log("tool-call-budget", "budget exhausted: calls=%d budget=%d (%.0f%%)",
			t.totalCalls, budget, pct*100)
		return fmt.Sprintf(
			"[tool budget] Tool call budget exhausted (%d / %d calls, %.0f%%). "+
				"Stopping to prevent further execution. Summarize what was accomplished and what remains.",
			t.totalCalls, budget, pct*100,
		), true
	}

	// Urgent warning at 95%
	if pct >= toolCallBudgetUrgentThreshold && !t.warn95Given {
		t.warn95Given = true
		debug.Log("tool-call-budget", "95%% reached: calls=%d budget=%d",
			t.totalCalls, budget)
		return fmt.Sprintf(
			"[tool budget] Tool call budget almost exhausted (%d / %d calls, %.0f%%). "+
				"Finalize current work, run verification, and prepare to conclude. Avoid starting new exploration.",
			t.totalCalls, budget, pct*100,
		), false
	}

	// Early warning at 80%
	if pct >= toolCallBudgetWarnThreshold && !t.warn80Given {
		t.warn80Given = true
		debug.Log("tool-call-budget", "80%% reached: calls=%d budget=%d",
			t.totalCalls, budget)
		return fmt.Sprintf(
			"[tool budget] Tool call budget 80%% consumed (%d / %d calls, %.0f%%). "+
				"Prioritize remaining work — batch operations and avoid redundant reads.",
			t.totalCalls, budget, pct*100,
		), false
	}

	return "", false
}

// deriveDefaultBudget computes a reasonable default tool call budget from
// the iteration limit. Returns defaultToolCallBudgetUnlimited when maxIter
// is 0 (unlimited), providing a safety net for autopilot/cron runs.
func deriveDefaultBudget(maxIter int) int {
	if maxIter <= 0 {
		return defaultToolCallBudgetUnlimited
	}
	return maxIter * defaultToolCallBudgetMultiplier
}
