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
	os.WriteFile(fp, []byte("world"), 0644)

	cp, err := m.Undo("user")
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

	if len(m.List()) != 0 {
		t.Error("expected empty list after undo")
	}
}

func TestUndoEmpty(t *testing.T) {
	m := NewManager(50)
	_, err := m.Undo("user")
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

	os.WriteFile(fp, []byte("c"), 0644)

	reverted, err := m.Revert(cp1.ID)
	if err != nil {
		t.Fatalf("Revert failed: %v", err)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "a" {
		t.Errorf("expected a, got %s", string(data))
	}

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

	m.Save("/src/main.go", "old1", "new1", "edit_file")
	m.Save("/src/main.go", "new1", "new2", "edit_file")
	m.Save("/src/util.go", "oldutil", "newutil", "write_file")
	m.SaveWithExistence("/src/new.go", "", "fresh", "write_file", false)

	files := m.ModifiedFiles()
	if len(files) != 3 {
		t.Fatalf("expected 3 unique files, got %d", len(files))
	}

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

	_, err := m.Undo("user")
	if err != nil {
		t.Fatalf("Undo failed: %v", err)
	}
	data, _ := os.ReadFile(fp)
	if string(data) != "hello" {
		t.Fatalf("expected hello after undo, got %s", string(data))
	}

	if !m.CanRedo() {
		t.Error("expected CanRedo to be true after undo")
	}

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

	if len(m.List()) != 1 {
		t.Errorf("expected 1 checkpoint after redo, got %d", len(m.List()))
	}

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

	m.Undo("user")
	m.Undo("user")
	data, _ := os.ReadFile(fp)
	if string(data) != "a" {
		t.Fatalf("expected a after 2 undos, got %s", string(data))
	}

	m.Redo()
	data, _ = os.ReadFile(fp)
	if string(data) != "b" {
		t.Errorf("expected b after 1 redo, got %s", string(data))
	}
	m.Redo()
	data, _ = os.ReadFile(fp)
	if string(data) != "c" {
		t.Errorf("expected c after 2 redos, got %s", string(data))
	}

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

	m.Undo("user")

	if !m.CanRedo() {
		t.Fatal("expected CanRedo true after undo")
	}

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

	m.StartRun("run-1")
	m.Save(fp1, "original-a", "modified-a", "edit_file")
	os.WriteFile(fp1, []byte("modified-a"), 0644)
	m.Save(fp2, "original-b", "modified-b", "write_file")
	os.WriteFile(fp2, []byte("modified-b"), 0644)

	data, _ := os.ReadFile(fp1)
	if string(data) != "modified-a" {
		t.Fatalf("expected modified-a, got %s", data)
	}

	reverted, err := m.UndoRun()
	if err != nil {
		t.Fatalf("UndoRun failed: %v", err)
	}
	if len(reverted) != 2 {
		t.Fatalf("expected 2 reverted checkpoints, got %d", len(reverted))
	}

	data, _ = os.ReadFile(fp1)
	if string(data) != "original-a" {
		t.Errorf("expected original-a, got %s", data)
	}
	data, _ = os.ReadFile(fp2)
	if string(data) != "original-b" {
		t.Errorf("expected original-b, got %s", data)
	}

	if len(m.List()) != 0 {
		t.Errorf("expected 0 checkpoints after UndoRun, got %d", len(m.List()))
	}
}

func TestUndoRunRespectsRunBoundaries(t *testing.T) {
	m := NewManager(50)
	dir := t.TempDir()

	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte("v0"), 0644)

	m.StartRun("run-1")
	m.Save(fp, "v0", "v1", "edit_file")
	os.WriteFile(fp, []byte("v1"), 0644)

	m.StartRun("run-2")
	m.Save(fp, "v1", "v2", "edit_file")
	os.WriteFile(fp, []byte("v2"), 0644)
	m.Save(fp, "v2", "v3", "edit_file")
	os.WriteFile(fp, []byte("v3"), 0644)

	reverted, err := m.UndoRun()
	if err != nil {
		t.Fatalf("UndoRun failed: %v", err)
	}
	if len(reverted) != 1 {
		t.Fatalf("expected 1 reverted file (deduped), got %d", len(reverted))
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "v1" {
		t.Errorf("expected v1, got %s", data)
	}

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

	m.UndoRun()

	if !m.CanRedo() {
		t.Fatal("expected CanRedo after UndoRun")
	}

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
	if len(reverted) != 1 {
		t.Fatalf("expected 1 reverted file (deduped), got %d", len(reverted))
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "baseline" {
		t.Errorf("expected 'baseline', got %s", data)
	}
}

func TestRevertWithFilesReturnsAllRevertedFiles(t *testing.T) {
	dir := t.TempDir()
	fa := filepath.Join(dir, "a.txt")
	fb := filepath.Join(dir, "b.txt")
	m := NewManager(50)

	cp1 := m.Save(fa, "a1", "a2", "edit_file")
	m.Save(fb, "b1", "b2", "edit_file")
	m.Save(fa, "a2", "a3", "edit_file")

	_, files, err := m.RevertWithFiles(cp1.ID)
	if err != nil {
		t.Fatalf("RevertWithFiles failed: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("revert of cp1 must report BOTH co-existing files, got %v", files)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f] = true
	}
	if !got[fa] || !got[fb] {
		t.Fatalf("files must include a and b, got %v", files)
	}
	if data, _ := os.ReadFile(fa); string(data) != "a1" {
		t.Errorf("a.txt must be back at a1, got %q", data)
	}
	if data, _ := os.ReadFile(fb); string(data) != "b1" {
		t.Errorf("b.txt must be back at b1, got %q", data)
	}
}

func TestRevertWithFilesSourceAgent(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(50)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := m.Save(path, "v1", "v2", "edit_file")
	if _, _, err := m.RevertWithFilesSource(cp.ID, "agent"); err != nil {
		t.Fatalf("agent-sourced revert failed: %v", err)
	}
	if len(m.corrections) == 0 {
		t.Fatal("revert must record a correction")
	}
	if got := m.corrections[len(m.corrections)-1].Source; got != "agent" {
		t.Fatalf("agent revert must record Source=agent, got %q", got)
	}

	cp2 := m.Save(path, "v2", "v3", "edit_file")
	if _, _, err := m.RevertWithFiles(cp2.ID); err != nil {
		t.Fatal(err)
	}
	if got := m.corrections[len(m.corrections)-1].Source; got != "user" {
		t.Fatalf("plain Revert must keep Source=user, got %q", got)
	}
}
