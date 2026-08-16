package checkpoint

// Feature tests for GitHub issue #554 (ver-41 probe findings B/C/D/E).
// Rejected items (A/F) intentionally untouched.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B: write_file on a missing path must undo by REMOVING the file, not by
// writing back OldContent ("") — which left a stray 0-byte file.
func TestIssue554B_UndoRemovesCreatedFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "created.go")

	m := NewManager(10)
	// computeFileChange records existed=false for a missing path.
	m.SaveWithExistence(fp, "", "package main", "write_file", false)
	// Simulate the tool having written the file.
	if err := os.WriteFile(fp, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Undo(); err != nil {
		t.Fatalf("Undo failed: %v", err)
	}

	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		data, _ := os.ReadFile(fp)
		t.Fatalf("created file must be removed on undo; stat err=%v content=%q", err, string(data))
	}
}

// B (Revert path): reverting to a file-creation checkpoint must also remove
// the file.
func TestIssue554B_RevertRemovesCreatedFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "created.txt")

	m := NewManager(10)
	cp := m.SaveWithExistence(fp, "", "hello", "write_file", false)
	if err := os.WriteFile(fp, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Revert(cp.ID); err != nil {
		t.Fatalf("Revert failed: %v", err)
	}
	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Fatalf("created file must be removed on revert; stat err=%v", err)
	}
}

// B (UndoRun path): a run that created a file must remove it when the run is
// undone (writeBaselines baseline is "absent", not an empty buffer).
func TestIssue554B_UndoRunRemovesCreatedFile(t *testing.T) {
	dir := t.TempDir()
	created := filepath.Join(dir, "new.txt")
	existing := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(existing, []byte("v0"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(10)
	m.StartRun("run-b")
	m.SaveWithExistence(created, "", "fresh", "write_file", false)
	_ = os.WriteFile(created, []byte("fresh"), 0644)
	m.Save(existing, "v0", "v1", "edit_file")
	_ = os.WriteFile(existing, []byte("v1"), 0644)

	if _, err := m.UndoRun(); err != nil {
		t.Fatalf("UndoRun failed: %v", err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("run-created file must be removed by UndoRun; stat err=%v", err)
	}
	if data, _ := os.ReadFile(existing); string(data) != "v0" {
		t.Fatalf("existing file must be restored to v0; got %q", string(data))
	}
}

// C: a pre-existing EMPTY file (empty __init__.py / .gitkeep) is an edit, not
// a creation: IsNew must be false and undo must restore the empty file (not
// delete it).
func TestIssue554C_PreexistingEmptyFileIsNotNew(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "__init__.py")
	if err := os.WriteFile(fp, nil, 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(10)
	// Save (not SaveWithExistence) assumes the file existed — that is the
	// correct record for an edit of a pre-existing empty file.
	m.Save(fp, "", "x = 1", "write_file")
	_ = os.WriteFile(fp, []byte("x = 1"), 0644)

	files := m.ModifiedFiles()
	if len(files) != 1 {
		t.Fatalf("expected 1 modified file, got %d", len(files))
	}
	if files[0].IsNew {
		t.Error("pre-existing empty file must not be reported as new (IsNew)")
	}

	if _, err := m.Undo(); err != nil {
		t.Fatalf("Undo failed: %v", err)
	}
	if _, err := os.Stat(fp); err != nil {
		t.Fatalf("pre-existing empty file must be restored (exist, 0 bytes), not deleted: %v", err)
	}
	if data, _ := os.ReadFile(fp); len(data) != 0 {
		t.Fatalf("restored file must be empty; got %q", string(data))
	}
}

// E: Revert must clear the redo stack. v0->v1->v2, Undo (redo pending), then
// Revert to the first checkpoint jumps to v0; a subsequent Redo must NOT
// re-apply v2 — the user explicitly rolled back past that state.
func TestIssue554E_RevertClearsRedoStack(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(fp, []byte("v0"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(10)
	cp1 := m.Save(fp, "v0", "v1", "edit_file")
	_ = os.WriteFile(fp, []byte("v1"), 0644)
	m.Save(fp, "v1", "v2", "edit_file")
	_ = os.WriteFile(fp, []byte("v2"), 0644)

	// Undo v2 -> v1 (v2 goes to redo stack).
	if _, err := m.Undo(); err != nil {
		t.Fatalf("first Undo failed: %v", err)
	}
	// Jump straight back to the pre-edit state v0.
	if _, err := m.Revert(cp1.ID); err != nil {
		t.Fatalf("Revert failed: %v", err)
	}
	if data, _ := os.ReadFile(fp); string(data) != "v0" {
		t.Fatalf("expected v0 after Revert; got %q", string(data))
	}

	// Redo must now be impossible: v2 was a state the user rolled back past.
	if _, err := m.Redo(); err == nil {
		if data, _ := os.ReadFile(fp); string(data) == "v2" {
			t.Fatal("Redo re-applied v2 after explicit Revert — user-rejected state restored")
		}
		t.Fatal("Redo should fail after Revert cleared the redo stack")
	}
	if data, _ := os.ReadFile(fp); string(data) != "v0" {
		t.Fatalf("file must remain at v0; got %q", string(data))
	}
}

// D: when a run's checkpoints are split into non-contiguous segments in the
// global list (e.g. after a partial-failure cleanup removed mid-list entries),
// runSegmentIndices only sees the tail segment. Undoing just that segment
// would restore a mid-run state as if it were the pre-run baseline. UndoRun
// must refuse instead.
func TestIssue554D_UndoRunRefusesNonContiguousSegments(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	for fp, content := range map[string]string{a: "a0", b: "b0"} {
		if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	m := NewManager(10)
	// run-1 edits a; then run-2 edits b; then run-1 resumes and edits a
	// again — the global list is [r1, r2, r1], a mid-list segment split of
	// the kind a partial writeBaselines failure leaves behind.
	m.StartRun("run-1")
	m.Save(a, "a0", "a1", "edit_file")
	_ = os.WriteFile(a, []byte("a1"), 0644)
	m.StartRun("run-2")
	m.Save(b, "b0", "b1", "edit_file")
	_ = os.WriteFile(b, []byte("b1"), 0644)
	m.StartRun("run-1")
	m.Save(a, "a1", "a2", "edit_file")
	_ = os.WriteFile(a, []byte("a2"), 0644)

	_, err := m.UndoRun()
	if err == nil {
		t.Fatal("UndoRun must refuse when the run's checkpoints are non-contiguous")
	}
	if !strings.Contains(err.Error(), "non-contiguous") {
		t.Fatalf("error should explain the segment split; got: %v", err)
	}
	// Disk must be untouched by the refused undo.
	if data, _ := os.ReadFile(a); string(data) != "a2" {
		t.Fatalf("refused UndoRun must not touch disk; a=%q", string(data))
	}
	if data, _ := os.ReadFile(b); string(data) != "b1" {
		t.Fatalf("refused UndoRun must not touch disk; b=%q", string(data))
	}
}
