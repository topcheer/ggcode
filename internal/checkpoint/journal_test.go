package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestManager builds a persistent Manager whose journal lives under dir,
// so tests never touch the real user config directory.
func newTestManager(t *testing.T, dir string) *Manager {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		t.Fatal(err)
	}
	return openPersistentManagerAt(50, filepath.Join(dir, "j.jsonl"))
}

// TestPersistentManagerRoundTrip verifies that checkpoints recorded through
// a persistent Manager survive reconstruction from the journal: the replayed
// manager can undo the same edit, and first-existence tracking is preserved.
func TestPersistentManagerRoundTrip(t *testing.T) {
	base := t.TempDir()
	f := filepath.Join(base, "a.txt")
	if err := os.WriteFile(f, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}

	jdir := filepath.Join(base, "journal")
	m1 := newTestManager(t, jdir)
	m1.StartRun("run-1")
	m1.SaveWithExistence(f, "v1", "v2", "edit_file", true)

	// Reconstruct: a fresh Manager must replay the journal to the same state.
	m2 := newTestManager(t, jdir)
	if got := len(m2.checkpoints); got != 1 {
		t.Fatalf("replayed checkpoints = %d, want 1", got)
	}
	if got := m2.fileExisted[f]; got != true {
		t.Fatalf("replayed fileExisted[%s] = %v, want true", f, got)
	}

	// Undo on the replayed manager restores v1 on disk.
	if _, err := m2.Undo("user"); err != nil {
		t.Fatalf("Undo after replay: %v", err)
	}
	if b, _ := os.ReadFile(f); string(b) != "v1" {
		t.Fatalf("file = %q, want %q", b, "v1")
	}

	// The undo event itself is journaled: a third manager sees zero live
	// checkpoints and one recorded correction.
	m3 := newTestManager(t, jdir)
	if got := len(m3.checkpoints); got != 0 {
		t.Fatalf("post-undo checkpoints = %d, want 0", got)
	}
	corr := m3.RecentCorrections()
	if len(corr) != 1 || corr[0].Source != "user" {
		t.Fatalf("replayed corrections = %+v, want 1 with Source=user", corr)
	}
}

// TestPersistentManagerClear verifies evClear resets replayed state.
func TestPersistentManagerClear(t *testing.T) {
	base := t.TempDir()
	f := filepath.Join(base, "b.txt")

	jdir := filepath.Join(base, "journal")
	m1 := newTestManager(t, jdir)
	m1.SaveWithExistence(f, "", "new", "write_file", false)
	m1.Clear()

	m2 := newTestManager(t, jdir)
	if got := len(m2.checkpoints); got != 0 {
		t.Fatalf("checkpoints after replayed Clear = %d, want 0", got)
	}
	if m2.fileExisted != nil {
		t.Fatal("fileExisted not reset by replayed Clear")
	}
}

// TestPlainManagerJournalsNothing asserts the opt-in property: a Manager
// from NewManager never creates a journal file even when it mutates state.
func TestPlainManagerJournalsNothing(t *testing.T) {
	m := NewManager(50)
	f := filepath.Join(t.TempDir(), "c.txt")
	m.SaveWithExistence(f, "old", "new", "edit_file", true)
	if m.journal != nil {
		t.Fatal("plain Manager has a journal writer")
	}
}
