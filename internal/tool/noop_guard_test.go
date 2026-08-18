package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFile_NoOpIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	original := "package main\n\nfunc foo() {}\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordWrite(path) // avoid stale-read false positive

	mtimeBefore, _ := os.Stat(path)

	ef := EditFile{WorkingDir: dir}
	args, _ := json.Marshal(map[string]string{
		"file_path": path,
		"old_text":  "func foo() {}",
		"new_text":  "func foo() {}", // identical → no-op
	})
	result, err := ef.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "No change") {
		t.Fatalf("expected no-op message, got: %s", result.Content)
	}

	// Verify file mtime did not change (no write occurred).
	mtimeAfter, _ := os.Stat(path)
	if !mtimeBefore.ModTime().Equal(mtimeAfter.ModTime()) {
		t.Fatalf("file was written despite being a no-op edit (mtime changed)")
	}
}

func TestEditFile_RealChangeStillWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	original := "package main\n\nfunc foo() {}\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordWrite(path)

	ef := EditFile{WorkingDir: dir}
	args, _ := json.Marshal(map[string]string{
		"file_path": path,
		"old_text":  "func foo() {}",
		"new_text":  "func bar() {}",
	})
	result, err := ef.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Content, "No change") {
		t.Fatalf("expected actual edit, not a no-op")
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "func bar() {}") {
		t.Fatalf("file content was not updated, got: %s", string(data))
	}
}

func TestWriteFile_NoOpIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordWrite(path)

	mtimeBefore, _ := os.Stat(path)

	wf := WriteFile{WorkingDir: dir}
	args, _ := json.Marshal(map[string]string{
		"path":    path,
		"content": content, // identical → no-op
	})
	result, err := wf.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "No change") {
		t.Fatalf("expected no-op message, got: %s", result.Content)
	}

	mtimeAfter, _ := os.Stat(path)
	if !mtimeBefore.ModTime().Equal(mtimeAfter.ModTime()) {
		t.Fatalf("file was written despite being a no-op write (mtime changed)")
	}
}

func TestWriteFile_NewFileStillWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	wf := WriteFile{WorkingDir: dir}
	args, _ := json.Marshal(map[string]string{
		"path":    path,
		"content": "hello world",
	})
	result, err := wf.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Content, "No change") {
		t.Fatalf("new file should not be treated as no-op")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file was not created: %v", err)
	}
}

func TestMultiFileWrite_NoOpIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skip.txt")
	content := "unchanged content\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordWrite(path)

	mfw := MultiFileWrite{WorkingDir: dir}
	args, _ := json.Marshal(map[string]any{
		"files": []map[string]string{
			{"path": path, "content": content}, // identical → skip
		},
	})
	result, err := mfw.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "skipped=1") {
		t.Fatalf("expected skipped=1 in summary, got: %s", result.Content)
	}
}

func TestMultiEditFile_NoOpIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	original := "package main\n\nfunc foo() {}\n\nfunc bar() {}\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordWrite(path) // avoid stale-read false positive

	mtimeBefore, _ := os.Stat(path)

	me := MultiEditFile{WorkingDir: dir}
	args, _ := json.Marshal(map[string]any{
		"file_path": path,
		"edits": []map[string]string{
			{"old_text": "func foo() {}", "new_text": "func foo() {}"}, // identical → no-op
			{"old_text": "func bar() {}", "new_text": "func bar() {}"}, // identical → no-op
		},
	})
	result, err := me.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "No change") {
		t.Fatalf("expected no-op message, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "all 2 edits") {
		t.Fatalf("expected edit count in message, got: %s", result.Content)
	}

	// Verify file mtime did not change (no write occurred).
	mtimeAfter, _ := os.Stat(path)
	if !mtimeBefore.ModTime().Equal(mtimeAfter.ModTime()) {
		t.Fatalf("file was written despite being a no-op edit (mtime changed)")
	}
}

func TestMultiEditFile_RealChangeStillWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	original := "package main\n\nfunc foo() {}\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordWrite(path)

	me := MultiEditFile{WorkingDir: dir}
	args, _ := json.Marshal(map[string]any{
		"file_path": path,
		"edits": []map[string]string{
			{"old_text": "func foo() {}", "new_text": "func bar() {}"}, // real change
		},
	})
	result, err := me.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Content, "No change") {
		t.Fatalf("expected actual edits, not a no-op")
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "func bar() {}") {
		t.Fatalf("file content was not updated, got: %s", string(data))
	}
}
