package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAnchorStoreBindsWorkspace pins #5498: stores are lazily bound to the
// agent's workspace (NOT the process cwd) on SetWorkingDir, and rebind when
// the workspace changes. The live deployment shape is cwd != workspace (ACP
// server: cwd=/, workspace=project dir).
func TestAnchorStoreBindsWorkspace(t *testing.T) {
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

	if a.anchorStore == nil {
		t.Fatal("SetWorkingDir must lazily bind anchorStore")
	}
	if a.anchorStore.boundDir != workspace {
		t.Fatalf("anchorStore must bind to workspace %q, got %q", workspace, a.anchorStore.boundDir)
	}
	if a.selfModel == nil || a.selfModel.boundDir != workspace {
		t.Fatalf("selfModel must bind to workspace %q", workspace)
	}

	// Record through the agent-facing write path: the JSON must land under
	// the workspace, not the process cwd.
	a.anchorStore.ObserveUserMessage("Never push directly to main.")
	if _, err := os.Stat(filepath.Join(workspace, ".ggcode", "anchors.json")); err != nil {
		t.Fatalf("anchors.json must live under the workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(procDir, ".ggcode", "anchors.json")); err == nil {
		t.Fatal("cwd must NOT receive anchors.json (the #5498 regression)")
	}

	// Rebind on workspace change.
	workspace2 := t.TempDir()
	a.SetWorkingDir(workspace2)
	if a.anchorStore.boundDir != workspace2 {
		t.Fatalf("anchorStore must rebind on SetWorkingDir, got %q", a.anchorStore.boundDir)
	}
}
