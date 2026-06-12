package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileBasic(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("hello world\nline 2\n"), 0o644)

	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": fp})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "hello world") {
		t.Errorf("expected file content: %s", result.Content)
	}
}

func TestReadFileWithOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "lines.txt")
	var content string
	for i := 0; i < 100; i++ {
		content += "line content\n"
	}
	os.WriteFile(fp, []byte(content), 0o644)

	r := ReadFile{}
	input, _ := json.Marshal(map[string]interface{}{"path": fp, "offset": 10, "limit": 5})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Should contain 5 lines
	t.Logf("result length: %d", len(result.Content))
}

func TestReadFileNotFound(t *testing.T) {
	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": "/nonexistent/file.txt"})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing file")
	}
}

func TestReadFileInvalidInput(t *testing.T) {
	r := ReadFile{}
	result, err := r.Execute(context.Background(), json.RawMessage(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestReadFileEmptyPath(t *testing.T) {
	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": ""})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for empty path")
	}
}

func TestReadFileDirectory(t *testing.T) {
	dir := t.TempDir()
	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": dir})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for directory path")
	}
}

func TestReadFileSandboxCheck(t *testing.T) {
	r := ReadFile{SandboxCheck: func(path string) bool { return false }}
	input, _ := json.Marshal(map[string]string{"path": "/forbidden/secret.txt"})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected sandbox error")
	}
}
