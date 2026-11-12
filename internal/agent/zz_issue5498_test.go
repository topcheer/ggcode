package agent

import (
	"os"
	"testing"
)

// TestSelfModelBindsWorkspace pins the surviving half of #5498: the selfModel
// store is lazily bound to the agent's workspace (NOT the process cwd) on
// SetWorkingDir, and rebinds when the workspace changes. The live deployment
// shape is cwd != workspace (ACP server: cwd=/, workspace=project dir).
// The anchor-store half of the #5498/#5500 binding was removed wholesale in
// #5514; git history covers it.
func TestSelfModelBindsWorkspace(t *testing.T) {
	workspace := t.TempDir()
	procDir := t.TempDir() // pretend this is the process cwd
	if procDir == workspace {
		t.Skip("need distinct dirs")
	}

	// Old behavior: cwd-anchored writes. Simulate by chdir to procDir so any
	// residual relative-path write would land in procDir.
	oldWd, _ := os.Getwd()
	if err := os.Chdir(procDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd)

	a := &Agent{} // minimal agent; NewAgent wiring covered by field checks
	a.SetWorkingDir(workspace)

	if a.selfModel == nil {
		t.Fatal("SetWorkingDir must lazily bind selfModel")
	}
	if a.selfModel.boundDir != workspace {
		t.Fatalf("selfModel must bind to workspace %q, got %q", workspace, a.selfModel.boundDir)
	}

	// Rebind on workspace change.
	workspace2 := t.TempDir()
	a.SetWorkingDir(workspace2)
	if a.selfModel.boundDir != workspace2 {
		t.Fatalf("selfModel must rebind on SetWorkingDir, got %q", a.selfModel.boundDir)
	}
}
