package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndList(t *testing.T) {
	m := NewManager(50)

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("hello"), 0644)

	m.Save(fp, "hello", "world", "edit_file")

	list := m.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(list))
	}
	if list[0].ToolCall != "edit_file" {
		t.Errorf("expected tool_call edit_file, got %s", list[0].ToolCall)
	}
	if list[0].OldContent != "hello" {
		t.Errorf("expected old_content hello, got %s", list[0].OldContent)
	}
	if list[0].ID == "" {
		t.Error("expected non-empty ID")
	}
}

func TestUndo(t *testing.T) {
	m := NewManager(50)

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("hello"), 0644)

	m.Save(fp, "hello", "world", "edit_file")
	// Simulate the write that happened
	os.WriteFile(fp, []byte("world"), 0644)

	cp, err := m.Undo()
	if err != nil {
		t.Fatalf("Undo failed: %v", err)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "hello" {
		t.Errorf("expected file content hello, got %s", string(data))
	}

	if cp.NewContent != "world" {
		t.Errorf("expected new_content world, got %s", cp.NewContent)
	}

	// List should now be empty
	if len(m.List()) != 0 {
		t.Error("expected empty list after undo")
	}
}

func TestUndoEmpty(t *testing.T) {
	m := NewManager(50)
	_, err := m.Undo()
	if err == nil {
		t.Fatal("expected error for undo on empty")
	}
}

func TestRevert(t *testing.T) {
	m := NewManager(50)

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("a"), 0644)

	cp1 := m.Save(fp, "a", "b", "edit_file")
	m.Save(fp, "b", "c", "edit_file")

	// Write c to file
	os.WriteFile(fp, []byte("c"), 0644)

	reverted, err := m.Revert(cp1.ID)
	if err != nil {
		t.Fatalf("Revert failed: %v", err)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "a" {
		t.Errorf("expected a, got %s", string(data))
	}

	// Only the reverted checkpoint should remain
	if reverted.OldContent != "a" {
		t.Errorf("expected old_content a, got %s", reverted.OldContent)
	}
}

func TestRevertNotFound(t *testing.T) {
	m := NewManager(50)
	_, err := m.Revert("nonexistent")
	if err == nil {
		t.Fatal("expected error for revert nonexistent")
	}
}

func TestMaxCheckpoints(t *testing.T) {
	m := NewManager(3)
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")

	for i := 0; i < 5; i++ {
		m.Save(fp, string(rune('a'+i)), string(rune('b'+i)), "edit_file")
	}

	list := m.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 checkpoints, got %d", len(list))
	}
	// Oldest should be evicted, first should be 'c'->'d'
	if list[0].OldContent != "c" {
		t.Errorf("expected oldest old_content c, got %s", list[0].OldContent)
	}
}

func TestClear(t *testing.T) {
	m := NewManager(50)
	m.Save("a.txt", "a", "b", "edit_file")
	m.Clear()
	if len(m.List()) != 0 {
		t.Error("expected empty after clear")
	}
}

func TestModifiedFiles(t *testing.T) {
	m := NewManager(50)

	// Three edits to two files + one new file
	m.Save("/src/main.go", "old1", "new1", "edit_file")
	m.Save("/src/main.go", "new1", "new2", "edit_file")
	m.Save("/src/util.go", "oldutil", "newutil", "write_file")
	// #554: Save assumes the file existed; a file-creating write_file must
	// record existed=false or the checkpoint is not marked IsNew.
	m.SaveWithExistence("/src/new.go", "", "fresh", "write_file", false)

	files := m.ModifiedFiles()
	if len(files) != 3 {
		t.Fatalf("expected 3 unique files, got %d", len(files))
	}

	// Order should be by first modification: main.go, util.go, new.go
	if files[0].Path != "/src/main.go" {
		t.Errorf("expected main.go first, got %s", files[0].Path)
	}
	if files[0].Edits != 2 {
		t.Errorf("expected 2 edits for main.go, got %d", files[0].Edits)
	}
	if files[0].IsNew {
		t.Error("main.go should not be new")
	}

	if files[1].Path != "/src/util.go" || files[1].Edits != 1 {
		t.Errorf("unexpected util.go: %+v", files[1])
	}

	if files[2].Path != "/src/new.go" || !files[2].IsNew {
		t.Errorf("expected new.go to be marked as new: %+v", files[2])
	}

	// LastTool should reflect the most recent edit for each file
	if files[0].LastTool != "edit_file" {
		t.Errorf("expected last tool edit_file for main.go, got %s", files[0].LastTool)
	}
	if files[2].LastTool != "write_file" {
		t.Errorf("expected last tool write_file for new.go, got %s", files[2].LastTool)
	}
}

func TestModifiedFilesEmpty(t *testing.T) {
	m := NewManager(50)
	files := m.ModifiedFiles()
	if len(files) != 0 {
		t.Errorf("expected empty slice, got %d items", len(files))
	}
}

func TestRedo(t *testing.T) {
	m := NewManager(50)

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("hello"), 0644)

	m.Save(fp, "hello", "world", "edit_file")
	os.WriteFile(fp, []byte("world"), 0644)

	// Undo
	_, err := m.Undo()
	if err != nil {
		t.Fatalf("Undo failed: %v", err)
	}
	data, _ := os.ReadFile(fp)
	if string(data) != "hello" {
		t.Fatalf("expected hello after undo, got %s", string(data))
	}

	// CanRedo should be true now
	if !m.CanRedo() {
		t.Error("expected CanRedo to be true after undo")
	}

	// Redo
	cp, err := m.Redo()
	if err != nil {
		t.Fatalf("Redo failed: %v", err)
	}
	data, _ = os.ReadFile(fp)
	if string(data) != "world" {
		t.Errorf("expected world after redo, got %s", string(data))
	}
	if cp.NewContent != "world" {
		t.Errorf("expected new_content world, got %s", cp.NewContent)
	}

	// Checkpoint should be back on the main list
	if len(m.List()) != 1 {
		t.Errorf("expected 1 checkpoint after redo, got %d", len(m.List()))
	}

	// CanRedo should be false now
	if m.CanRedo() {
		t.Error("expected CanRedo to be false after redo")
	}
}

func TestRedoEmpty(t *testing.T) {
	m := NewManager(50)
	_, err := m.Redo()
	if err == nil {
		t.Fatal("expected error for redo on empty redo stack")
	}
}

func TestRedoMultiple(t *testing.T) {
	m := NewManager(50)

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("a"), 0644)

	m.Save(fp, "a", "b", "edit_file")
	os.WriteFile(fp, []byte("b"), 0644)
	m.Save(fp, "b", "c", "edit_file")
	os.WriteFile(fp, []byte("c"), 0644)

	// Undo twice
	m.Undo() // c -> b
	m.Undo() // b -> a
	data, _ := os.ReadFile(fp)
	if string(data) != "a" {
		t.Fatalf("expected a after 2 undos, got %s", string(data))
	}

	// Redo twice
	m.Redo() // a -> b
	data, _ = os.ReadFile(fp)
	if string(data) != "b" {
		t.Errorf("expected b after 1 redo, got %s", string(data))
	}
	m.Redo() // b -> c
	data, _ = os.ReadFile(fp)
	if string(data) != "c" {
		t.Errorf("expected c after 2 redos, got %s", string(data))
	}

	// Redo again should fail
	_, err := m.Redo()
	if err == nil {
		t.Fatal("expected error for redo when redo stack is empty")
	}
}

func TestSaveClearsRedoStack(t *testing.T) {
	m := NewManager(50)

	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("a"), 0644)

	m.Save(fp, "a", "b", "edit_file")
	os.WriteFile(fp, []byte("b"), 0644)

	m.Undo() // b -> a, pushes to redo stack

	if !m.CanRedo() {
		t.Fatal("expected CanRedo true after undo")
	}

	// New edit should clear redo stack
	m.Save(fp, "a", "d", "edit_file")

	if m.CanRedo() {
		t.Error("expected CanRedo false after new Save (redo stack should be cleared)")
	}
}

func TestUndoRunMultipleFiles(t *testing.T) {
	m := NewManager(50)
	dir := t.TempDir()

	fp1 := filepath.Join(dir, "a.go")
	fp2 := filepath.Join(dir, "b.go")
	os.WriteFile(fp1, []byte("original-a"), 0644)
	os.WriteFile(fp2, []byte("original-b"), 0644)

	// Simulate a run that edits two files
	m.StartRun("run-1")
	m.Save(fp1, "original-a", "modified-a", "edit_file")
	os.WriteFile(fp1, []byte("modified-a"), 0644)
	m.Save(fp2, "original-b", "modified-b", "write_file")
	os.WriteFile(fp2, []byte("modified-b"), 0644)

	// Verify both files are modified
	data, _ := os.ReadFile(fp1)
	if string(data) != "modified-a" {
		t.Fatalf("expected modified-a, got %s", data)
	}

	// UndoRun should revert both files to pre-run state
	reverted, err := m.UndoRun()
	if err != nil {
		t.Fatalf("UndoRun failed: %v", err)
	}
	if len(reverted) != 2 {
		t.Fatalf("expected 2 reverted checkpoints, got %d", len(reverted))
	}

	// Both files should be back to original
	data, _ = os.ReadFile(fp1)
	if string(data) != "original-a" {
		t.Errorf("expected original-a, got %s", data)
	}
	data, _ = os.ReadFile(fp2)
	if string(data) != "original-b" {
		t.Errorf("expected original-b, got %s", data)
	}

	// Checkpoints should be empty
	if len(m.List()) != 0 {
		t.Errorf("expected 0 checkpoints after UndoRun, got %d", len(m.List()))
	}
}

func TestUndoRunRespectsRunBoundaries(t *testing.T) {
	m := NewManager(50)
	dir := t.TempDir()

	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte("v0"), 0644)

	// Run 1
	m.StartRun("run-1")
	m.Save(fp, "v0", "v1", "edit_file")
	os.WriteFile(fp, []byte("v1"), 0644)

	// Run 2 (same file, further edits)
	m.StartRun("run-2")
	m.Save(fp, "v1", "v2", "edit_file")
	os.WriteFile(fp, []byte("v2"), 0644)
	m.Save(fp, "v2", "v3", "edit_file")
	os.WriteFile(fp, []byte("v3"), 0644)

	// UndoRun should only revert run-2 (v3 -> v1), not run-1
	// Only 1 entry returned because both run-2 checkpoints are for the same file
	// (deduplicated by file path).
	reverted, err := m.UndoRun()
	if err != nil {
		t.Fatalf("UndoRun failed: %v", err)
	}
	if len(reverted) != 1 {
		t.Fatalf("expected 1 reverted file (deduped), got %d", len(reverted))
	}

	// File should be at v1 (pre-run-2 state), not v0
	data, _ := os.ReadFile(fp)
	if string(data) != "v1" {
		t.Errorf("expected v1, got %s", data)
	}

	// Run 1 checkpoints should still be present
	if len(m.List()) != 1 {
		t.Errorf("expected 1 checkpoint remaining from run-1, got %d", len(m.List()))
	}
}

func TestUndoRunRedoStack(t *testing.T) {
	m := NewManager(50)
	dir := t.TempDir()

	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte("original"), 0644)

	m.StartRun("run-1")
	m.Save(fp, "original", "changed", "edit_file")
	os.WriteFile(fp, []byte("changed"), 0644)

	// UndoRun pushes checkpoints onto redo stack
	m.UndoRun()

	if !m.CanRedo() {
		t.Fatal("expected CanRedo after UndoRun")
	}

	// Redo should re-apply the change
	cp, err := m.Redo()
	if err != nil {
		t.Fatalf("Redo failed: %v", err)
	}
	if cp.NewContent != "changed" {
		t.Errorf("expected NewContent 'changed', got %s", cp.NewContent)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "changed" {
		t.Errorf("expected 'changed' after redo, got %s", data)
	}
}

func TestUndoRunEmpty(t *testing.T) {
	m := NewManager(50)
	_, err := m.UndoRun()
	if err == nil {
		t.Fatal("expected error for UndoRun with no checkpoints")
	}
}

func TestUndoRunSingleFileMultipleEdits(t *testing.T) {
	m := NewManager(50)
	dir := t.TempDir()

	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte("baseline"), 0644)

	m.StartRun("run-1")
	// Multiple edits to the same file within one run
	m.Save(fp, "baseline", "edit1", "edit_file")
	os.WriteFile(fp, []byte("edit1"), 0644)
	m.Save(fp, "edit1", "edit2", "edit_file")
	os.WriteFile(fp, []byte("edit2"), 0644)
	m.Save(fp, "edit2", "edit3", "edit_file")
	os.WriteFile(fp, []byte("edit3"), 0644)

	reverted, err := m.UndoRun()
	if err != nil {
		t.Fatalf("UndoRun failed: %v", err)
	}
	// Should only write the file once (to baseline)
	if len(reverted) != 1 {
		t.Fatalf("expected 1 reverted file (deduped), got %d", len(reverted))
	}

	// File should be at baseline
	data, _ := os.ReadFile(fp)
	if string(data) != "baseline" {
		t.Errorf("expected 'baseline', got %s", data)
	}
}
