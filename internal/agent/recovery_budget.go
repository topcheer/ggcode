package agent

import (
	"fmt"
	"sync"
)

// recoveryBudget implements the per-tool recovery budget from self-healing
// orchestration research (r21; Suresh Babu & Agrawal, arXiv:2606.01416):
// retry-only baselines plateau at 94.5% under moderate fault intensity while
// classified, budgeted recovery reaches 98.8% - because a fault that will not
// resolve itself (malformed args, wrong tool) burns budget identically on
// every retry. The budget forces the cheapest action to give way to the next
// rung of the cost-ordered ladder:
//
//	retry (<=2) - substitute (<=4) - escalate (terminal)
//
// Scope: per-tool CONSECUTIVE failures (any success resets), at most one
// hint per rung per tool per run. Non-blocking guidance injection - the
// model still decides; this only stops feeding it "just retry" after the
// data says retry is exhausted.
type recoveryBudget struct {
	mu sync.Mutex
	// consec tracks consecutive failures per tool.
	consec map[string]int
	// firedSub / firedEsc ensure each rung's hint fires at most once per
	// tool per run (budget spent - no repeated nagging).
	firedSub map[string]bool
	firedEsc map[string]bool
}

func newRecoveryBudget() *recoveryBudget {
	return &recoveryBudget{
		consec:   make(map[string]int),
		firedSub: make(map[string]bool),
		firedEsc: make(map[string]bool),
	}
}

// observeSuccess resets the consecutive-failure count for a tool.
func (rb *recoveryBudget) observeSuccess(toolName string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	delete(rb.consec, toolName)
}

// observeFailure records a failure and returns an escalation hint when a
// budget rung is crossed ("" = within budget, keep retry-class guidance).
func (rb *recoveryBudget) observeFailure(toolName string) string {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.consec[toolName]++
	n := rb.consec[toolName]

	// Terminal rung: sustained failure - retrying is provably futile.
	if n >= 5 && !rb.firedEsc[toolName] {
		rb.firedEsc[toolName] = true
		return fmt.Sprintf("[Recovery budget exhausted] %s has failed %d consecutive times. "+
			"Stop retrying this approach: report the blocker, state what you tried, and ask the user (or switch to a fundamentally different method).", toolName, n)
	}
	// Substitute rung: retry budget spent.
	if n >= 3 && !rb.firedSub[toolName] {
		rb.firedSub[toolName] = true
		return fmt.Sprintf("[Recovery budget: substitute] %s has failed %d consecutive times. "+
			"Plain retries are exhausted - substitute a different tool/approach for this subtask instead of repeating the call.", toolName, n)
	}
	return ""
}

// counts snapshots consecutive failures (test/telemetry helper).
func (rb *recoveryBudget) counts(toolName string) int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.consec[toolName]
}
