package agent

import "github.com/topcheer/ggcode/internal/debug"

// toolCallBudget is an opt-in hard cap on the actual tool invocations of one
// run (per-call gate: parallel tool batching cannot smuggle extra calls under
// one LLM turn, #5013).
//
// It is armed ONLY by an explicit budget: config `tool_call_budget` or a
// spawn's `max_tool_calls`. There is no default cap and nothing is ever
// injected into the conversation: earlier revisions derived a default from
// maxIter (500 calls for unlimited runs, maxIter*8 otherwise) and injected
// "[tool budget] ..." nudges at 80% / 95% into the context, which made the
// model wind down productive work early and cut long runs short. Both were
// removed on request - the loop keeps working until the task is done, the
// model finishes, or the caller cancels. Session cost budgets (token / cost /
// wall-clock) are a separate mechanism and unaffected.
type toolCallBudget struct {
	totalCalls int
	budget     int
	stopGiven  bool
}

func newToolCallBudget() *toolCallBudget {
	return &toolCallBudget{}
}

// SetBudget arms the gate; 0 (or negative) disables it.
func (t *toolCallBudget) SetBudget(budget int) {
	t.budget = budget
}

func (t *toolCallBudget) reset() {
	t.totalCalls = 0
	t.stopGiven = false
}

func (t *toolCallBudget) record() {
	t.totalCalls++
}

// check reports whether the explicit budget is exhausted. It never produces a
// message for the conversation: the caller ends the run with a user-facing
// summary instead of steering the model.
func (t *toolCallBudget) check() (exhausted bool) {
	if t.budget <= 0 || t.stopGiven || t.totalCalls < t.budget {
		return false
	}
	t.stopGiven = true
	debug.Log("tool-call-budget", "explicit budget exhausted: calls=%d budget=%d", t.totalCalls, t.budget)
	return true
}
