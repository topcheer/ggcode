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
	// #5374: identical hint text repeated inside one tool result is pure
	// noise (an N-file multi_file_edit used to append N verbatim lines).
	// Keep the first occurrence; count the skip as suppressed for stats.
	if result.Content != "" && strings.Contains(result.Content, hint) {
		a.guidanceBudget.suppressed++
		debug.Log("guidance-budget", "suppressing duplicate tool-result hint (%d suppressed this turn)",
			a.guidanceBudget.suppressed)
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
