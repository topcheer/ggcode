package agent

import (
	"testing"
)

// #3233 regression: the #3227 RuleStore singleton froze at first-build
// directory; a mid-session working-dir change (enter_worktree adoption /
// SetWorkingDir) kept persisting rules into the OLD (main-tree) directory,
// polluting the main rule library with worktree-specific failure shapes.

// TestSetWorkingDir_ReanchorsRuleStore: after SetWorkingDir the singleton
// must rebuild anchored to the NEW directory, and the user-edit observer
// cache must drop with it.
func TestSetWorkingDir_ReanchorsRuleStore(t *testing.T) {
	mainDir := t.TempDir()
	wtDir := t.TempDir()

	a := &Agent{workingDir: mainDir}

	rs1 := a.getRuleStore()
	if rs1 == nil {
		t.Fatal("getRuleStore returned nil for non-empty workingDir")
	}
	// Pre-warm the observer cache so we can prove it drops on re-anchor.
	obs1 := a.getUserEditObserver()
	if obs1 == nil {
		t.Fatal("getUserEditObserver returned nil")
	}

	a.SetWorkingDir(wtDir)

	rs2 := a.getRuleStore()
	if rs2 == nil {
		t.Fatal("getRuleStore nil after working-dir change")
	}
	if rs2 == rs1 {
		t.Fatal("ruleStore singleton survived a working-dir change - rules would persist into the old directory (#3233)")
	}
	obs2 := a.getUserEditObserver()
	if obs2 == obs1 {
		t.Error("userEditObserver survived the re-anchor - it would keep writing to the abandoned store")
	}
}

// TestSetWorkingDir_SameDirKeepsStore: a no-op dir change must not thrash
// the singleton (and must not lose in-memory accumulated rules).
func TestSetWorkingDir_SameDirKeepsStore(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{workingDir: dir}
	rs1 := a.getRuleStore()
	a.SetWorkingDir(dir) // same dir
	if rs2 := a.getRuleStore(); rs2 != rs1 {
		t.Error("same-dir SetWorkingDir must not rebuild the singleton")
	}
}

// TestResetRuleStoreLocked_DropsObserver: the reset primitive itself
// clears both caches (callers hold a.mu).
func TestResetRuleStoreLocked_DropsObserver(t *testing.T) {
	a := &Agent{workingDir: t.TempDir()}
	_ = a.getRuleStore()
	_ = a.getUserEditObserver()
	a.mu.Lock()
	a.resetRuleStoreLocked()
	a.mu.Unlock()
	if a.ruleStore != nil || a.userEditObs != nil {
		t.Errorf("resetRuleStoreLocked left residue: ruleStore=%v userEditObs=%v", a.ruleStore != nil, a.userEditObs != nil)
	}
}
