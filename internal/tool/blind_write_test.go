package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBlindWriteWarning_NewFile verifies that no warning is shown for a brand
// new file (no existing content to lose).
func TestBlindWriteWarning_NewFile(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "new_file.go")

	warning := blindWriteWarning(path)
	if warning != "" {
		t.Errorf("expected empty warning for new file, got: %s", warning)
	}
}

// TestBlindWriteWarning_UnreadExistingFile verifies that a warning IS shown
// when the agent is overwriting an existing file it has never read.
func TestBlindWriteWarning_UnreadExistingFile(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "existing.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	warning := blindWriteWarning(path)
	if warning == "" {
		t.Error("expected warning for unread existing file")
	}
	if !strings.Contains(warning, "without having read it") {
		t.Errorf("warning should mention blind write, got: %s", warning)
	}
	if !strings.Contains(warning, "existing.go") {
		t.Errorf("warning should include file path, got: %s", warning)
	}
}

// TestBlindWriteWarning_AfterRead verifies that no warning is shown after
// the agent has read the file (via RecordRead).
func TestBlindWriteWarning_AfterRead(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "read.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(path)

	warning := blindWriteWarning(path)
	if warning != "" {
		t.Errorf("expected no warning after read, got: %s", warning)
	}
}

// TestBlindWriteWarning_AfterWrite verifies that no warning is shown after
// the agent has already written the file (it knows the contents).
func TestBlindWriteWarning_AfterWrite(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "written.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordWrite(path)

	warning := blindWriteWarning(path)
	if warning != "" {
		t.Errorf("expected no warning after write, got: %s", warning)
	}
}

// TestWriteFile_BlindOverwriteWarning is an integration test verifying that
// write_file includes the blind overwrite warning in its output when
// overwriting an unread file.
func TestWriteFile_BlindOverwriteWarning(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "target.txt")

	// Pre-create a file with content (simulating an existing file the agent
	// has not read).
	if err := os.WriteFile(path, []byte("important existing content"), 0644); err != nil {
		t.Fatal(err)
	}

	wf := WriteFile{WorkingDir: tmpDir}
	input, _ := json.Marshal(map[string]string{
		"path":    path,
		"content": "replacement content",
	})

	result, err := wf.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "without having read it") {
		t.Errorf("expected blind write warning in output, got: %s", result.Content)
	}
}

// TestWriteFile_NoWarningAfterRead verifies that write_file does NOT show the
// warning when the file was previously read.
func TestWriteFile_NoWarningAfterRead(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "read_first.txt")

	if err := os.WriteFile(path, []byte("original content"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(path)

	wf := WriteFile{WorkingDir: tmpDir}
	input, _ := json.Marshal(map[string]string{
		"path":    path,
		"content": "new content",
	})

	result, err := wf.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Content, "without having read it") {
		t.Errorf("should not warn after read, got: %s", result.Content)
	}
}

// TestMultiFileWrite_BlindOverwriteWarning verifies that multi_file_write
// includes the blind overwrite warning for unread files.
func TestMultiFileWrite_BlindOverwriteWarning(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	readPath := filepath.Join(tmpDir, "read_file.go")
	blindPath := filepath.Join(tmpDir, "blind_file.go")

	// Pre-create both files.
	os.WriteFile(readPath, []byte("package main\n"), 0644)
	os.WriteFile(blindPath, []byte("package main\n// important content\n"), 0644)

	// Record read for one file only.
	defaultFileTracker.RecordRead(readPath)

	mfw := MultiFileWrite{WorkingDir: tmpDir}
	input, _ := json.Marshal(map[string]interface{}{
		"files": []map[string]string{
			{"path": readPath, "content": "package main\n// updated\n"},
			{"path": blindPath, "content": "package main\n// replaced\n"},
		},
	})

	result, err := mfw.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "blind_file.go") || !strings.Contains(result.Content, "without having read it") {
		t.Errorf("expected blind write warning for blind_file.go, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "read_file.go") && strings.Contains(strings.SplitAfter(result.Content, "blind_file.go")[0], "read_file.go") &&
		strings.Contains(strings.SplitAfter(result.Content, "read_file.go")[0], "without having read it") {
		t.Errorf("should not warn for read_file.go, got: %s", result.Content)
	}
}

// TestHasBeenSeen verifies the tracker's HasBeenSeen method.
func TestHasBeenSeen(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "seen.go")
	os.WriteFile(path, []byte("x"), 0644)

	if tracker.HasBeenSeen(path) {
		t.Error("expected HasBeenSeen=false for unseen file")
	}

	tracker.RecordRead(path)
	if !tracker.HasBeenSeen(path) {
		t.Error("expected HasBeenSeen=true after RecordRead")
	}
}
