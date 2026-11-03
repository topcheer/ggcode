package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestWriteFile_BlindOverwriteWarning(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "target.txt")

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

func TestMultiFileWrite_BlindOverwriteWarning(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	readPath := filepath.Join(tmpDir, "read_file.go")
	blindPath := filepath.Join(tmpDir, "blind_file.go")

	os.WriteFile(readPath, []byte("package main\n"), 0644)
	os.WriteFile(blindPath, []byte("package main\n// important content\n"), 0644)

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
