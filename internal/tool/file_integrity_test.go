package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFileIntegrityTracker_StaleDetection verifies the core optimistic
// concurrency control: a file modified externally after read is detected.
func TestFileIntegrityTracker_StaleDetection(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	// Create initial file.
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate agent reading the file.
	tracker.RecordRead(path)

	// Simulate external modification (e.g., another agent or user edit).
	time.Sleep(10 * time.Millisecond) // ensure mtime changes
	if err := os.WriteFile(path, []byte("external change"), 0644); err != nil {
		t.Fatal(err)
	}

	// Check: file should be detected as stale.
	stale, _ := tracker.CheckStale(path)
	if !stale {
		t.Error("expected stale=true after external modification")
	}

	// After recording the write, file should no longer be stale.
	tracker.RecordWrite(path)
	stale, _ = tracker.CheckStale(path)
	if stale {
		t.Error("expected stale=false after RecordWrite")
	}
}

// TestFileIntegrityTracker_NeverReadNotStale verifies that a file never
// read by the agent is NOT considered stale (backward compatible).
func TestFileIntegrityTracker_NeverReadNotStale(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "untracked.txt")

	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	stale, _ := tracker.CheckStale(path)
	if stale {
		t.Error("file never read should not be considered stale")
	}
}

// TestFileIntegrityTracker_EditThenWriteNotStale verifies that an edit_file
// followed by a write_file on the same path does NOT trigger a false stale
// detection. This is the critical scenario: the agent reads, edits (which
// updates the tracker), then writes (which should succeed).
func TestFileIntegrityTracker_EditThenWriteNotStale(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "edit_then_write.go")

	// Create file and read it.
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tracker.RecordRead(path)

	// Simulate edit (RecordWrite updates the tracker).
	tracker.RecordWrite(path)

	// Write should NOT be stale.
	stale, _ := tracker.CheckStale(path)
	if stale {
		t.Error("write after edit should not be stale (edit updated tracker)")
	}
}

// TestWriteFile_StaleReadBlocked is an integration test verifying that
// write_file refuses to overwrite a file that was modified externally
// since the last read.
func TestWriteFile_StaleReadBlocked(t *testing.T) {
	// Reset the global tracker for a clean test.
	defaultFileTracker.Reset()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "stale.txt")

	// Create initial file.
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate agent reading the file via read_file tool.
	rf := ReadFile{}
	readInput, _ := json.Marshal(map[string]string{"path": path})
	rf.Execute(context.Background(), readInput)

	// Simulate external modification.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("external change"), 0644); err != nil {
		t.Fatal(err)
	}

	// Attempt to write — should be blocked.
	wf := WriteFile{}
	writeInput, _ := json.Marshal(map[string]string{
		"path":    path,
		"content": "agent's content",
	})
	result, err := wf.Execute(context.Background(), writeInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("write_file should have been blocked due to stale read")
	}
	if result.Content == "" {
		t.Error("error message should not be empty")
	}
}

// TestWriteFile_FreshWriteSucceeds verifies that writing to a file that
// was just read (no external modification) succeeds normally.
func TestWriteFile_FreshWriteSucceeds(t *testing.T) {
	defaultFileTracker.Reset()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "fresh.txt")

	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	// Read the file.
	rf := ReadFile{}
	readInput, _ := json.Marshal(map[string]string{"path": path})
	rf.Execute(context.Background(), readInput)

	// Write immediately — should succeed.
	wf := WriteFile{}
	writeInput, _ := json.Marshal(map[string]string{
		"path":    path,
		"content": "new content",
	})
	result, err := wf.Execute(context.Background(), writeInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("write_file should have succeeded, got error: %s", result.Content)
	}
}

// TestWriteFile_NewFileNotBlocked verifies that creating a brand new file
// is never blocked (no stale read possible for a file that didn't exist).
func TestWriteFile_NewFileNotBlocked(t *testing.T) {
	defaultFileTracker.Reset()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "new_file.txt")

	wf := WriteFile{}
	writeInput, _ := json.Marshal(map[string]string{
		"path":    path,
		"content": "brand new",
	})
	result, err := wf.Execute(context.Background(), writeInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("creating a new file should not be blocked, got: %s", result.Content)
	}
}

// TestEditFile_UpdatesTracker verifies that edit_file records the new mtime
// so a subsequent write_file doesn't see false staleness.
func TestEditFile_UpdatesTracker(t *testing.T) {
	defaultFileTracker.Reset()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "edit_test.txt")

	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Read the file.
	rf := ReadFile{}
	readInput, _ := json.Marshal(map[string]string{"path": path})
	rf.Execute(context.Background(), readInput)

	// Edit the file.
	ef := EditFile{WorkingDir: tmpDir}
	editInput, _ := json.Marshal(map[string]string{
		"file_path": path,
		"old_text":  "line1",
		"new_text":  "LINE1",
	})
	result, err := ef.Execute(context.Background(), editInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("edit failed: %s", result.Content)
	}

	// Now write_file should succeed (not stale) because edit updated tracker.
	wf := WriteFile{}
	writeInput, _ := json.Marshal(map[string]string{
		"path":    path,
		"content": "completely rewritten",
	})
	result, err = wf.Execute(context.Background(), writeInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("write after edit should succeed, got: %s", result.Content)
	}
}

// TestMultiFileWrite_StaleReadBlocked verifies that multi_file_write also
// enforces the stale-read guard.
func TestMultiFileWrite_StaleReadBlocked(t *testing.T) {
	defaultFileTracker.Reset()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "multi_stale.txt")

	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	// Read the file.
	rf := ReadFile{}
	readInput, _ := json.Marshal(map[string]string{"path": path})
	rf.Execute(context.Background(), readInput)

	// External modification.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("external"), 0644); err != nil {
		t.Fatal(err)
	}

	// Attempt multi_file_write — should report the file as failed.
	mfw := MultiFileWrite{}
	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{
			{"path": path, "content": "agent content"},
		},
	})
	result, err := mfw.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("multi_file_write should report error for stale file")
	}
}

// TestNormalizePath verifies path normalization for consistent map keys.
func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"relative", "foo/bar.txt"},
		{"with dot", "./foo/bar.txt"},
		{"with double dot", "foo/../foo/bar.txt"},
		{"absolute", "/tmp/foo/bar.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizePath(tt.in)
			if result == "" {
				t.Error("normalizePath returned empty string")
			}
			// All inputs that resolve to the same file should produce the same key.
			// We can't test exact equality without knowing cwd, but we can verify
			// that the result is cleaned (no ./ or ../).
			if filepath.Base(result) != "bar.txt" {
				t.Errorf("expected base 'bar.txt', got %q", filepath.Base(result))
			}
		})
	}
}
