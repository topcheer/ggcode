package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3690_BatchGateBaselineRelativeChecks(t *testing.T) {
	dir := t.TempDir()

	f := filepath.Join(dir, "drifted.go")
	os.WriteFile(f, []byte("package x\nvar A = 1\n"), 0o644)
	if blockers := dryRunValidateBatch([]fileEditPlan{{Path: f, OldContent: "package x\nvar A = 1\n", NewContent: ""}}); len(blockers) == 0 {
		t.Fatal("refreshed-baseline empty-loss must be blocked (old non-empty, new empty)")
	}

	f2 := filepath.Join(dir, "conflict.go")
	os.WriteFile(f2, []byte("package x\n"), 0o644)
	withMarkers := "package x\n<<<<<<< HEAD\nx\n=======\ny\n>>>>>>> b\n"
	if blockers := dryRunValidateBatch([]fileEditPlan{{Path: f2, OldContent: "package x\n", NewContent: withMarkers}}); len(blockers) == 0 {
		t.Fatal("marker-adding edit must be blocked on the refreshed baseline")
	}

	if blockers := dryRunValidateBatch([]fileEditPlan{{Path: f, OldContent: "package x\nvar A = 1\n", NewContent: "package x\nvar A = 2\n"}}); len(blockers) != 0 {
		t.Fatalf("clean drifted baseline must pass, got: %v", blockers)
	}
}
