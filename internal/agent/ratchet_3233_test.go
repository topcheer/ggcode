package agent

import (
	"testing"
)

func TestSetWorkingDir_ReanchorsRuleStore(t *testing.T) {
	mainDir := t.TempDir()
	wtDir := t.TempDir()

	a := &Agent{workingDir: mainDir}

	rs1 := a.getRuleStore()
	if rs1 == nil {
		t.Fatal("getRuleStore returned nil for non-empty workingDir")
	}

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

func TestSetWorkingDir_SameDirKeepsStore(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{workingDir: dir}
	rs1 := a.getRuleStore()
	a.SetWorkingDir(dir)
	if rs2 := a.getRuleStore(); rs2 != rs1 {
		t.Error("same-dir SetWorkingDir must not rebuild the singleton")
	}
}

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
