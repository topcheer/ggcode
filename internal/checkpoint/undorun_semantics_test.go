package checkpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUndoRunRefusesWhenRunBaselineEvicted reproduces issue #517 Bug B:
// with maxCheckpoints=3 and five edits to one file in a single run (v0..v5),
// FIFO eviction removes the run's earliest checkpoints. UndoRun used to
// silently treat the earliest *surviving* checkpoint (OldContent="v2") as
// the pre-run baseline and roll the file back to a mid-run state. It must
// now refuse without writing anything to disk.
func TestUndoRunRefusesWhenRunBaselineEvicted(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(fp, []byte("v0"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(3)
	m.StartRun("run-1")
	for i := 0; i < 5; i++ {
		oldV, newV := fmt.Sprintf("v%d", i), fmt.Sprintf("v%d", i+1)
		if err := os.WriteFile(fp, []byte(newV), 0644); err != nil {
			t.Fatal(err)
		}
		m.Save(fp, oldV, newV, "edit_file")
	}

	reverted, err := m.UndoRun()
	if err == nil {
		t.Fatalf("UndoRun must refuse when the run's baseline was evicted; got success with %d reverts", len(reverted))
	}
	if !strings.Contains(err.Error(), "evicted") {
		t.Errorf("error should explain the eviction: %v", err)
	}
	if len(reverted) != 0 {
		t.Errorf("refusal must not partially revert; got %d reverts", len(reverted))
	}
	// Disk must keep the post-run state, not a mid-run baseline.
	data, _ := os.ReadFile(fp)
	if string(data) != "v5" {
		t.Errorf("file must stay at v5 after refusal; got %q (silently rolled back to mid-run state)", string(data))
	}
	// Refusal must not pollute the redo stack.
	if m.CanRedo() {
		t.Error("refusal must not touch the redo stack")
	}
	// Checkpoints are retained so the user can still single-step Undo.
	if got := len(m.List()); got != 3 {
		t.Errorf("checkpoints should be retained after refusal; got %d", got)
	}
}

// TestUndoRunEvictionSkipsCurrentRun verifies the Save-side half of the fix:
// eviction prefers entries from older runs, so a short current run keeps its
// pre-run baseline even when older checkpoints must be evicted to stay under
// the cap.
func TestUndoRunEvictionSkipsCurrentRun(t *testing.T) {
	dir := t.TempDir()
	x := filepath.Join(dir, "x.txt")
	y := filepath.Join(dir, "y.txt")
	os.WriteFile(x, []byte("x0"), 0644)
	os.WriteFile(y, []byte("y0"), 0644)

	m := NewManager(3)
	m.StartRun("run-0")
	os.WriteFile(x, []byte("x1"), 0644)
	m.Save(x, "x0", "x1", "edit_file")
	os.WriteFile(x, []byte("x2"), 0644)
	m.Save(x, "x1", "x2", "edit_file")

	m.StartRun("run-1")
	for i := 0; i < 3; i++ {
		oldV, newV := fmt.Sprintf("y%d", i), fmt.Sprintf("y%d", i+1)
		if err := os.WriteFile(y, []byte(newV), 0644); err != nil {
			t.Fatal(err)
		}
		m.Save(y, oldV, newV, "edit_file")
	}

	// run-0's checkpoints were evicted (not run-1's), so UndoRun for the
	// current run must still succeed and restore the true baseline "y0".
	reverted, err := m.UndoRun()
	if err != nil {
		t.Fatalf("UndoRun should succeed when the tail run was not split: %v", err)
	}
	if len(reverted) != 1 {
		t.Fatalf("expected 1 reverted file, got %d", len(reverted))
	}
	data, _ := os.ReadFile(y)
	if string(data) != "y0" {
		t.Errorf("y must roll back to pre-run baseline y0; got %q", string(data))
	}
	// run-0's file was not part of this run and must be untouched on disk.
	dx, _ := os.ReadFile(x)
	if string(dx) != "x2" {
		t.Errorf("x must stay at x2; got %q", string(dx))
	}
}

// TestUndoRunPartialFailureCleansAllRunCheckpoints reproduces issue #517
// Bug A: when UndoRun fails partway (bad dir is read-only), the cleanup loop
// used to delete only ONE checkpoint per already-reverted file, leaving
// mid-run entries that a later single-step Undo would re-apply on top of the
// rolled-back baseline.
func TestUndoRunPartialFailureCleansAllRunCheckpoints(t *testing.T) {
	goodDir := t.TempDir()
	badDir := t.TempDir()
	a := filepath.Join(goodDir, "a.txt")
	bad := filepath.Join(badDir, "b.txt")
	if err := os.WriteFile(a, []byte("v0"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("b0"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(50)
	m.StartRun("run-1")
	// The bad file is edited first so that UndoRun (which processes files
	// tail-first) reverts a.txt before hitting the unwritable b.txt.
	if err := os.WriteFile(bad, []byte("b1"), 0644); err != nil {
		t.Fatal(err)
	}
	m.Save(bad, "b0", "b1", "edit_file")
	for i := 0; i < 3; i++ {
		oldV, newV := fmt.Sprintf("v%d", i), fmt.Sprintf("v%d", i+1)
		if err := os.WriteFile(a, []byte(newV), 0644); err != nil {
			t.Fatal(err)
		}
		m.Save(a, oldV, newV, "edit_file")
	}

	if err := os.Chmod(badDir, 0555); err != nil {
		t.Skipf("cannot make bad dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(badDir, 0755) })
	// Skip when the environment does not enforce write protection (e.g. root).
	probe := filepath.Join(badDir, ".writeprobe")
	if err := os.WriteFile(probe, nil, 0644); err == nil {
		_ = os.Remove(probe)
		t.Skip("directory write protection not enforced (running as root?)")
	}

	reverted, err := m.UndoRun()
	if err == nil {
		t.Fatal("UndoRun should fail on the unwritable directory")
	}
	// a.txt was reverted to its pre-run baseline before the failure.
	if len(reverted) != 1 || reverted[0].FilePath != a {
		t.Fatalf("expected exactly a.txt to be reverted before the failure; got %+v", reverted)
	}
	data, _ := os.ReadFile(a)
	if string(data) != "v0" {
		t.Errorf("a must be rolled back to v0; got %q", string(data))
	}

	// Cleanup must have removed every a.txt checkpoint from this run.
	for _, cp := range m.List() {
		if cp.FilePath == a {
			t.Fatalf("residual checkpoint for reverted file %s: %+v", a, cp)
		}
	}

	// A later single-step Undo must not resurrect a mid-run state of a.txt.
	// The only remaining checkpoint targets the unwritable bad file, so Undo
	// fails without mutating anything.
	if _, err := m.Undo(); err == nil {
		t.Fatal("Undo should fail while the bad dir is read-only")
	}
	data, _ = os.ReadFile(a)
	if string(data) != "v0" {
		t.Errorf("a must still be at v0 after the later Undo; got %q (mid-run state re-applied)", string(data))
	}
}
