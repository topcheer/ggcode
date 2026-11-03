package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileIntegrityTracker_StaleDetection(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	tracker.RecordRead(path)

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("external change"), 0644); err != nil {
		t.Fatal(err)
	}

	stale, _ := tracker.CheckStale(path)
	if !stale {
		t.Error("expected stale=true after external modification")
	}

	tracker.RecordWrite(path)
	stale, _ = tracker.CheckStale(path)
	if stale {
		t.Error("expected stale=false after RecordWrite")
	}
}

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

func TestFileIntegrityTracker_EditThenWriteNotStale(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "edit_then_write.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tracker.RecordRead(path)

	tracker.RecordWrite(path)

	stale, _ := tracker.CheckStale(path)
	if stale {
		t.Error("write after edit should not be stale (edit updated tracker)")
	}
}

func TestWriteFile_StaleReadBlocked(t *testing.T) {

	defaultFileTracker.Reset()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "stale.txt")

	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	rf := ReadFile{}
	readInput, _ := json.Marshal(map[string]string{"path": path})
	rf.Execute(context.Background(), readInput)

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("external change"), 0644); err != nil {
		t.Fatal(err)
	}

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

func TestWriteFile_FreshWriteSucceeds(t *testing.T) {
	defaultFileTracker.Reset()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "fresh.txt")

	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	rf := ReadFile{}
	readInput, _ := json.Marshal(map[string]string{"path": path})
	rf.Execute(context.Background(), readInput)

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

func TestEditFile_UpdatesTracker(t *testing.T) {
	defaultFileTracker.Reset()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "edit_test.txt")

	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rf := ReadFile{}
	readInput, _ := json.Marshal(map[string]string{"path": path})
	rf.Execute(context.Background(), readInput)

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

func TestMultiFileWrite_StaleReadBlocked(t *testing.T) {
	defaultFileTracker.Reset()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "multi_stale.txt")

	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	rf := ReadFile{}
	readInput, _ := json.Marshal(map[string]string{"path": path})
	rf.Execute(context.Background(), readInput)

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("external"), 0644); err != nil {
		t.Fatal(err)
	}

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

			if filepath.Base(result) != "bar.txt" {
				t.Errorf("expected base 'bar.txt', got %q", filepath.Base(result))
			}
		})
	}
}

func TestStaleReadHint(t *testing.T) {

	orig := defaultFileTracker
	defer func() { defaultFileTracker = orig }()
	defaultFileTracker = NewFileIntegrityTracker()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "stale_test.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(path)

	hint := staleReadHint(path)
	if hint != "" {
		t.Errorf("expected empty hint for non-stale file, got %q", hint)
	}

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("package main\n// changed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	hint = staleReadHint(path)
	if hint == "" {
		t.Error("expected non-empty hint for stale file")
	}
	if !strings.Contains(hint, "externally") {
		t.Errorf("hint should mention 'externally', got %q", hint)
	}
}

func TestStaleReadHint_NeverTracked(t *testing.T) {
	orig := defaultFileTracker
	defer func() { defaultFileTracker = orig }()
	defaultFileTracker = NewFileIntegrityTracker()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "untracked.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	hint := staleReadHint(path)
	if hint != "" {
		t.Errorf("expected empty hint for never-tracked file, got %q", hint)
	}
}

func TestStaleReadHint_AfterWriteNotStale(t *testing.T) {
	orig := defaultFileTracker
	defer func() { defaultFileTracker = orig }()
	defaultFileTracker = NewFileIntegrityTracker()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "write_test.txt")
	if err := os.WriteFile(path, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(path)

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordWrite(path)

	hint := staleReadHint(path)
	if hint != "" {
		t.Errorf("expected empty hint after RecordWrite, got %q", hint)
	}
}

func TestNormalizePathResolvesSymlinkAliases(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if normalizePath(real) != normalizePath(link) {
		t.Fatalf("alias must normalize to one key: real=%q link=%q", normalizePath(real), normalizePath(link))
	}
}

func TestCheckStaleThroughAlias(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	tracker := NewFileIntegrityTracker()
	tracker.RecordRead(real)

	if err := os.WriteFile(link, []byte("v2-external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stale, _ := tracker.CheckStale(real); !stale {
		t.Fatal("stale check via real path missed external modification through alias (#1408)")
	}
}
