package agent

import (
	"strings"
	"sync"
)

type giveupRevertState struct {
	mu        sync.Mutex
	fired     bool
	sawGiveup bool
}

var giveupPatterns = []string{
	"isn't possible",
	"is not possible",
	"cannot be done",
	"can't be done",
	"no way to implement",
	"not feasible",
	"impossible to fix",
}

var giveupRollbackTools = map[string]bool{
	"git_revert":   true,
	"undo_edit":    true,
	"git_reset":    true,
	"git_checkout": true,
	"git_stash":    true,
}

func (g *giveupRevertState) markGiveupText(text string) {
	if g == nil || text == "" || len(text) > 64*1024 {
		return
	}
	lower := strings.ToLower(text)
	for _, p := range giveupPatterns {
		if strings.Contains(lower, p) {
			g.mu.Lock()
			g.sawGiveup = true
			g.mu.Unlock()
			return
		}
	}
}

func (a *Agent) recordGiveupText(text string) {
	a.giveupRevert.markGiveupText(text)
}

func (g *giveupRevertState) recordRollback() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fired || !g.sawGiveup {
		return false
	}
	g.fired = true
	return true
}

func (a *Agent) recordGiveupRollback() {
	if a.giveupRevert.recordRollback() {
		a.injectGuidance(giveupRevertGuidance())
	}
}

func giveupRevertGuidance() string {
	return "[Give-up + rollback observed: you declared the task not possible and rolled the tree back. " +
		"Before ending: state in your summary WHAT concrete approaches were tried and WHY they cannot work, " +
		"so the user can escalate with full context instead of re-discovering the same dead ends.]"
}
