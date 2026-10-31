package agent

// #3690 probe (seam-level): the multi-file leg now re-runs the batch gate on
// REFRESHED baselines after the #1786 drift window. The gate's
// baseline-relative checks (#3 empty-loss: non-empty old → empty new;
// #4 conflict-marker delta) are what the refreshed re-run makes meaningful.
// The full executeMultiFileTool flow needs agent scaffolding too heavy for a
// unit probe, so this pins the gate primitive the refreshed path calls.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3690_BatchGateBaselineRelativeChecks(t *testing.T) {
	dir := t.TempDir()
	// Empty-loss on a REFRESHED baseline: file drifted to real content, the
	// planned new content is empty -> blocked (the stale-baseline call with
	// old="" would have passed).
	f := filepath.Join(dir, "drifted.go")
	os.WriteFile(f, []byte("package x\nvar A = 1\n"), 0o644)
	if blockers := dryRunValidateBatch([]fileEditPlan{{Path: f, OldContent: "package x\nvar A = 1\n", NewContent: ""}}); len(blockers) == 0 {
		t.Fatal("refreshed-baseline empty-loss must be blocked (old non-empty, new empty)")
	}
	// Conflict-marker delta on a refreshed baseline: drifted old has none,
	// new adds markers -> blocked.
	f2 := filepath.Join(dir, "conflict.go")
	os.WriteFile(f2, []byte("package x\n"), 0o644)
	withMarkers := "package x\n<<<<<<< HEAD\nx\n=======\ny\n>>>>>>> b\n"
	if blockers := dryRunValidateBatch([]fileEditPlan{{Path: f2, OldContent: "package x\n", NewContent: withMarkers}}); len(blockers) == 0 {
		t.Fatal("marker-adding edit must be blocked on the refreshed baseline")
	}
	// Clean drifted pair passes.
	if blockers := dryRunValidateBatch([]fileEditPlan{{Path: f, OldContent: "package x\nvar A = 1\n", NewContent: "package x\nvar A = 2\n"}}); len(blockers) != 0 {
		t.Fatalf("clean drifted baseline must pass, got: %v", blockers)
	}
}
