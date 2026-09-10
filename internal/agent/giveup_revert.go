package agent

import (
	"strings"
	"sync"
)

// giveupRevertCheck is the narrow re-add of the removed premature_surrender
// detector (#1823 case 2). 387282a6 deleted the broad lexical give-up
// detector as noise, but that left the SURRENDER side of the
// persistence-vs-abandonment axis uncovered: an agent that declares a task
// impossible AND rolls the tree back in the same run abandons silently.
// The narrow trigger requires BOTH signals, which is why it is safe to run
// ungated (unlike the claimsSupervision family):
//   - give-up language in assistant text ("isn't possible", "cannot be
//     done", "no way to") — the errorStrategyLoop rerun-pattern does not
//     see this shape at all;
//   - an actual tree rollback observed in the same run (git_revert /
//     undo_edit / git_reset / git_checkout discarding changes).
//
// It fires at most once per run and only nudges the agent to state WHAT was
// tried and escalate — it never blocks the surrender itself.
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
	"git_revert":   true, // creates a reverting commit
	"undo_edit":    true, // rolls back the last edit
	"git_reset":    true, // may discard (mode-dependent; see arg check)
	"git_checkout": true, // may discard tree changes
	"git_stash":    true, // parks the work
}

// recordGiveupText is called with each assistant text turn.
// markGiveupText sets the language flag (exported lowercase for tests
// without a full Agent).
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

// recordGiveupRollback is called when a tool that rolls the tree back
// executes successfully in the same run.
// recordGiveupRollback completes the pairing: returns true exactly once
// per run when a rollback lands after observed give-up language.
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

// giveupRevertGuidance is split out so tests can pin the message text
// without a fully-constructed Agent (injectGuidance touches the context
// manager).
func giveupRevertGuidance() string {
	return "[Give-up + rollback observed: you declared the task not possible and rolled the tree back. " +
		"Before ending: state in your summary WHAT concrete approaches were tried and WHY they cannot work, " +
		"so the user can escalate with full context instead of re-discovering the same dead ends.]"
}
