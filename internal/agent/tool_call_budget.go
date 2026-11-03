package agent

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	toolCallBudgetWarnThreshold = 0.80

	toolCallBudgetUrgentThreshold = 0.95

	toolCallBudgetStopThreshold = 1.0

	defaultToolCallBudgetUnlimited = 500

	defaultToolCallBudgetMultiplier = 8
)

type toolCallBudget struct {
	totalCalls int

	budget int

	defaultBudget int

	warn80Given bool
	warn95Given bool
	stopGiven   bool
}

func newToolCallBudget() *toolCallBudget {
	return &toolCallBudget{}
}

func (t *toolCallBudget) SetBudget(budget int) {
	t.budget = budget
}

func (t *toolCallBudget) SetDefaultBudget(budget int) {
	t.defaultBudget = budget
}

func (t *toolCallBudget) effectiveBudget() int {
	if t.budget > 0 {
		return t.budget
	}
	return t.defaultBudget
}

func (t *toolCallBudget) reset() {
	t.totalCalls = 0
	t.warn80Given = false
	t.warn95Given = false
	t.stopGiven = false
}

func (t *toolCallBudget) record() {
	t.totalCalls++
}

func (t *toolCallBudget) check() (msg string, stop bool) {
	budget := t.effectiveBudget()
	if budget <= 0 {
		return "", false
	}

	pct := float64(t.totalCalls) / float64(budget)

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

func deriveDefaultBudget(maxIter int) int {
	if maxIter <= 0 {
		return defaultToolCallBudgetUnlimited
	}
	return maxIter * defaultToolCallBudgetMultiplier
}
