package agent

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// appendGuidance appends a detector hint to a tool result's content while
// charging the unified per-turn guidance budget (batch 3 of the guidance-noise
// cleanup). All direct `result.Content = result.Content + "\n\n" + hint`
// appends in the tool-result path previously bypassed the budget entirely -
// only injectGuidance went through it - so a busy turn could stack unlimited
// hints into tool results.
//
// Semantics (mirrors guidanceBudget.allowDeduped, #607 B2/B3):
//   - critical hints always pass;
//   - a hint whose tag was already injected this turn is deduplicated;
//   - beyond guidanceBudgetPerTurn non-critical hints per turn, suppressed.
//
// Returns true when the hint was appended.
func (a *Agent) appendGuidance(result *tool.Result, hint string) bool {
	if hint == "" || result == nil {
		return false
	}
	if !a.guidanceBudget.allowDeduped(hint) {
		debug.Log("guidance-budget", "suppressing tool-result hint (budget exceeded or duplicate tag, %d suppressed this turn)",
			a.guidanceBudget.suppressed)
		return false
	}
	if result.Content != "" {
		result.Content = result.Content + "\n\n" + hint
	} else {
		result.Content = hint
	}
	return true
}
