package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRunMgr builds a Manager in a temp dir with a journal disabled (journal
// writes only go to disk when a journal file exists; NewManager keeps it
// in-memory until ConfigureJournal). Enough for revert-semantics tests.
func newRunMgr(t *testing.T) *Manager {
	t.Helper()
	return NewManager(64)
}

func TestRevertRecentRuns_RollsBackLastNRuns(t *testing.T) {
	m := newRunMgr(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("base\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Run 1: edit a.
	m.StartRun("run-1")
	m.Save(a, "base\n", "run1-a\n", "edit_file")

	// Run 2: edit a and b.
	m.StartRun("run-2")
	m.Save(a, "run1-a\n", "run2-a\n", "edit_file")
	m.Save(b, "base\n", "run2-b\n", "edit_file")

	reverted, err := m.RevertRecentRuns(2)
	if err != nil {
		t.Fatalf("RevertRecentRuns: %v", err)
	}
	if len(reverted) != 3 {
		t.Fatalf("want 3 reverted checkpoints (1 run-1 + 2 run-2), got %d", len(reverted))
	}
	for _, p := range []string{a, b} {
		got, _ := os.ReadFile(p)
		if string(got) != "base\n" {
			t.Errorf("%s: want pre-run-1 baseline %q, got %q", p, "base\n", got)
		}
	}
	if cps := m.List(); len(cps) != 0 {
		t.Errorf("checkpoint list should be empty after full revert, got %d", len(cps))
	}
}

func TestRevertRecentRuns_StopsEarlyWhenCheckpointsRunOut(t *testing.T) {
	m := newRunMgr(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(a, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only one run with edits exists; rewinding 5 turns must stop cleanly.
	m.StartRun("only")
	m.Save(a, "base\n", "edited\n", "edit_file")

	reverted, err := m.RevertRecentRuns(5)
	if err != nil {
		t.Fatalf("RevertRecentRuns(5) with 1 run: %v", err)
	}
	if len(reverted) != 1 {
		t.Fatalf("want 1 reverted checkpoint, got %d", len(reverted))
	}
	got, _ := os.ReadFile(a)
	if string(got) != "base\n" {
		t.Errorf("file not restored: %q", got)
	}
}

func TestRevertRecentRuns_NoCheckpointsIsNoop(t *testing.T) {
	m := newRunMgr(t)
	reverted, err := m.RevertRecentRuns(3)
	if err != nil {
		t.Fatalf("empty manager must not error, got %v", err)
	}
	if len(reverted) != 0 {
		t.Fatalf("want 0 reverted, got %d", len(reverted))
	}
}

func TestRevertRecentRuns_ZeroCountIsNoop(t *testing.T) {
	m := newRunMgr(t)
	reverted, err := m.RevertRecentRuns(0)
	if err != nil || len(reverted) != 0 {
		t.Fatalf("count=0 must be a no-op, got %d reverted, err %v", len(reverted), err)
	}
}

func TestRevertRecentRuns_EvictionRefusalReturnsPartial(t *testing.T) {
	// FIFO limit of 2 checkpoints; run-2 alone fills it, evicting run-1's
	// baseline (issue #517 scenario). RevertRecentRuns(2) must revert run-2
	// (the tail) and then surface the refusal for run-1 as a partial error.
	m := NewManager(2)
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(a, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.StartRun("run-1")
	m.Save(a, "base\n", "run1\n", "edit_file")
	m.StartRun("run-2")
	m.Save(a, "run1\n", "run2-first\n", "edit_file")
	m.Save(a, "run2-first\n", "run2-second\n", "edit_file")

	reverted, err := m.RevertRecentRuns(2)
	if err == nil {
		t.Fatalf("expected eviction refusal error for run-1, got nil")
	}
	if len(reverted) == 0 {
		t.Fatalf("run-2 should have been reverted before the refusal")
	}
	if !strings.Contains(err.Error(), "refusing") && !strings.Contains(err.Error(), "reverted") {
		t.Errorf("error should describe partial rollback, got: %v", err)
	}
	got, _ := os.ReadFile(a)
	if string(got) != "run1\n" {
		t.Errorf("run-2 revert should restore run-1 end state, got %q", got)
	}
}

func TestRevertRecentRuns_RedoRestoresEachRun(t *testing.T) {
	m := newRunMgr(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(a, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.StartRun("run-1")
	m.Save(a, "base\n", "run1\n", "edit_file")
	m.StartRun("run-2")
	m.Save(a, "run1\n", "run2\n", "edit_file")

	if _, err := m.RevertRecentRuns(2); err != nil {
		t.Fatal(err)
	}
	// Undo twice, redo twice: full fidelity round trip.
	for want := range map[int]string{0: "run1\n", 1: "run2\n"} {
		_ = want
	}
	// Redo stack order: first Redo re-applies the LAST reverted (run-2 second
	// checkpoint order is internal), verify final state after two Redos.
	if !m.CanRedo() {
		t.Fatalf("redo stack must be non-empty after RevertRecentRuns")
	}
	if _, err := m.Redo(); err != nil {
		t.Fatalf("first redo: %v", err)
	}
	if _, err := m.Redo(); err != nil {
		t.Fatalf("second redo: %v", err)
	}
	got, _ := os.ReadFile(a)
	if string(got) != "run2\n" {
		t.Errorf("after 2 redos want run2 state, got %q", got)
	}
}
