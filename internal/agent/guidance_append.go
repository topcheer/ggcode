package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

func (a *Agent) appendGuidance(result *tool.Result, hint string) bool {
	if hint == "" || result == nil {
		return false
	}
	// Identical-text dedup FIRST: per-file detectors can emit the same hint
	// text for each file in one multi-file tool call; repeating it verbatim N
	// times is noise (issue #5374). Checking before the budget also means a
	// duplicate must not consume one of the finite per-turn budget slots.
	if result.Content != "" && strings.Contains(result.Content, hint) {
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
