package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUndoRecordsCorrection(t *testing.T) {
	m := NewManager(50)
	dir := t.TempDir()
	fp := filepath.Join(dir, "a.txt")
	os.WriteFile(fp, []byte("old"), 0644)

	m.StartRun("run-1")
	m.Save(fp, "old", "new", "edit_file")
	os.WriteFile(fp, []byte("new"), 0644)

	// No corrections before undo.
	if c := m.RecentCorrections(); c != nil {
		t.Fatalf("expected nil corrections before undo, got %v", c)
	}

	cp, err := m.Undo()
	if err != nil {
		t.Fatalf("Undo failed: %v", err)
	}

	corrections := m.RecentCorrections()
	if len(corrections) != 1 {
		t.Fatalf("expected 1 correction after undo, got %d", len(corrections))
	}
	c := corrections[0]
	if len(c.Files) != 1 || c.Files[0] != fp {
		t.Errorf("expected correction file %s, got %v", fp, c.Files)
	}
	if c.ToolCall != "edit_file" {
		t.Errorf("expected tool_call edit_file, got %s", c.ToolCall)
	}
	if c.RunID != "run-1" {
		t.Errorf("expected run_id run-1, got %s", c.RunID)
	}

	// Verify the returned checkpoint matches.
	if cp.FilePath != fp {
		t.Errorf("expected checkpoint file %s, got %s", fp, cp.FilePath)
	}
}

func TestUndoRunRecordsCorrection(t *testing.T) {
	m := NewManager(50)
	dir := t.TempDir()
	fp1 := filepath.Join(dir, "a.txt")
	fp2 := filepath.Join(dir, "b.txt")
	os.WriteFile(fp1, []byte("old1"), 0644)
	os.WriteFile(fp2, []byte("old2"), 0644)

	m.StartRun("run-1")
	m.Save(fp1, "old1", "new1", "edit_file")
	os.WriteFile(fp1, []byte("new1"), 0644)
	m.Save(fp2, "old2", "new2", "write_file")
	os.WriteFile(fp2, []byte("new2"), 0644)

	reverted, err := m.UndoRun()
	if err != nil {
		t.Fatalf("UndoRun failed: %v", err)
	}
	if len(reverted) != 2 {
		t.Fatalf("expected 2 reverted checkpoints, got %d", len(reverted))
	}

	corrections := m.RecentCorrections()
	if len(corrections) != 1 {
		t.Fatalf("expected 1 correction after undo-run, got %d", len(corrections))
	}
	c := corrections[0]
	if len(c.Files) != 2 {
		t.Errorf("expected 2 files in correction, got %d", len(c.Files))
	}
	// RunID should be the reverted run.
	if c.RunID != "run-1" {
		t.Errorf("expected run_id run-1, got %s", c.RunID)
	}
}

func TestClearCorrections(t *testing.T) {
	m := NewManager(50)
	dir := t.TempDir()
	fp := filepath.Join(dir, "a.txt")
	os.WriteFile(fp, []byte("old"), 0644)

	m.StartRun("run-1")
	m.Save(fp, "old", "new", "edit_file")
	os.WriteFile(fp, []byte("new"), 0644)

	if _, err := m.Undo(); err != nil {
		t.Fatalf("Undo failed: %v", err)
	}
	if len(m.RecentCorrections()) != 1 {
		t.Fatal("expected 1 correction")
	}

	m.ClearCorrections()
	if c := m.RecentCorrections(); c != nil {
		t.Fatalf("expected nil after ClearCorrections, got %v", c)
	}
}

func TestClearResetsCorrections(t *testing.T) {
	m := NewManager(50)
	dir := t.TempDir()
	fp := filepath.Join(dir, "a.txt")
	os.WriteFile(fp, []byte("old"), 0644)

	m.StartRun("run-1")
	m.Save(fp, "old", "new", "edit_file")
	os.WriteFile(fp, []byte("new"), 0644)
	if _, err := m.Undo(); err != nil {
		t.Fatalf("Undo failed: %v", err)
	}

	m.Clear()
	if c := m.RecentCorrections(); c != nil {
		t.Fatalf("expected nil after Clear, got %v", c)
	}
}
