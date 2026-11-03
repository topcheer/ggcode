package agent

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

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
