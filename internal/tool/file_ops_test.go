package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileOps_DeleteFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "deleteme.txt")
	if err := os.WriteFile(target, []byte("bye"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "delete", "source": target},
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("file should not exist after delete, got: %v", err)
	}
}

func TestFileOps_DeleteNonEmptyDir_WithoutRecursive(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "nested", "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "delete", "source": target},
		},
	})

	res, _ := tool.Execute(context.Background(), input)
	if !res.IsError {
		t.Fatalf("expected error for non-empty dir delete without recursive")
	}

	// Dir should still exist.
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("dir should still exist: %v", err)
	}
}

func TestFileOps_DeleteNonEmptyDir_WithRecursive(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "nested", "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "delete", "source": target, "recursive": true},
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dir should not exist after recursive delete")
	}
}

func TestFileOps_DeleteNonExistent_Skipped(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nope.txt")

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "delete", "source": target},
		},
	})

	res, _ := tool.Execute(context.Background(), input)
	if res.IsError {
		t.Fatalf("deleting non-existent file should not be an error")
	}

	var content fileOpsContent
	if err := json.Unmarshal([]byte(res.Content), &content); err != nil {
		t.Fatal(err)
	}
	if content.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", content.Skipped)
	}
}

func TestFileOps_Move(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "old.txt")
	dst := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "move", "source": src, "destination": dst},
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}

	// Source should be gone.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source should not exist after move")
	}
	// Destination should have the content.
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("error reading destination: %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

func TestFileOps_MoveToNewDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "file.txt")
	dst := filepath.Join(dir, "subdir", "file.txt")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "move", "source": src, "destination": dst},
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}

	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("destination should exist: %v", err)
	}
}

func TestFileOps_Mkdir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a", "b", "c")

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "mkdir", "source": target},
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory")
	}
}

func TestFileOps_MultipleOps(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	f2 := filepath.Join(dir, "f2.txt")
	newdir := filepath.Join(dir, "newdir")

	if err := os.WriteFile(f1, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "mkdir", "source": newdir},
			{"action": "move", "source": f1, "destination": filepath.Join(newdir, "f1.txt")},
			{"action": "delete", "source": f2},
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}

	var content fileOpsContent
	if err := json.Unmarshal([]byte(res.Content), &content); err != nil {
		t.Fatal(err)
	}
	if content.OK != 3 {
		t.Fatalf("expected 3 ok, got %d", content.OK)
	}
	if content.Errors != 0 {
		t.Fatalf("expected 0 errors, got %d", content.Errors)
	}

	// Verify file moved.
	if _, err := os.Stat(filepath.Join(newdir, "f1.txt")); err != nil {
		t.Fatalf("moved file should exist: %v", err)
	}
	// Verify file deleted.
	if _, err := os.Stat(f2); !os.IsNotExist(err) {
		t.Fatalf("f2 should be deleted")
	}
}

func TestFileOps_SandboxBlocked(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	// Sandbox that blocks everything.
	tool := FileOps{
		WorkingDir:   dir,
		SandboxCheck: func(path string) bool { return false },
	}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "delete", "source": target},
		},
	})

	res, _ := tool.Execute(context.Background(), input)
	if !res.IsError {
		t.Fatalf("expected error from sandbox block")
	}

	// File should still exist.
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("file should still exist after sandbox block: %v", err)
	}
}

func TestFileOps_InvalidAction(t *testing.T) {
	dir := t.TempDir()

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "copy", "source": filepath.Join(dir, "x")},
		},
	})

	res, _ := tool.Execute(context.Background(), input)
	if !res.IsError {
		t.Fatalf("expected error for invalid action")
	}
}

func TestFileOps_TrackerIntegration(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tracked.txt")

	defaultFileTracker.Reset()
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(target)

	// Verify it's tracked.
	if !defaultFileTracker.HasBeenSeen(target) {
		t.Fatal("file should be tracked before delete")
	}

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "delete", "source": target},
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}

	// After delete, the tracker should no longer track this path.
	if defaultFileTracker.HasBeenSeen(target) {
		t.Fatal("file should not be tracked after delete")
	}
}

func TestFileOps_MoveTrackerIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	defaultFileTracker.Reset()
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(src)

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "move", "source": src, "destination": dst},
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}

	// Source should be untracked, destination should be tracked.
	if defaultFileTracker.HasBeenSeen(src) {
		t.Fatal("source should not be tracked after move")
	}
	if !defaultFileTracker.HasBeenSeen(dst) {
		t.Fatal("destination should be tracked after move")
	}
}

func TestFileOps_EmptyOperations(t *testing.T) {
	tool := FileOps{WorkingDir: t.TempDir()}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{},
	})

	res, _ := tool.Execute(context.Background(), input)
	if !res.IsError {
		t.Fatalf("expected error for empty operations")
	}
}

func TestRemoveTracking(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	dir := t.TempDir()
	p := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	tracker.RecordRead(p)
	if !tracker.HasBeenSeen(p) {
		t.Fatal("should be tracked")
	}
	tracker.RemoveTracking(p)
	if tracker.HasBeenSeen(p) {
		t.Fatal("should not be tracked after RemoveTracking")
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
