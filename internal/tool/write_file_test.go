package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileBasic(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "output.txt")

	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": fp, "content": "hello world"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "hello world" {
		t.Errorf("content mismatch: %q", string(data))
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "overwrite.txt")
	os.WriteFile(fp, []byte("old content"), 0o644)

	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": fp, "content": "new content"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "new content" {
		t.Errorf("content mismatch: %q", string(data))
	}
}

func TestWriteFileEmptyContent(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "empty.txt")

	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": fp, "content": ""})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if len(data) != 0 {
		t.Errorf("expected empty file, got %q", string(data))
	}
}

func TestWriteFileInvalidInput(t *testing.T) {
	w := WriteFile{}
	result, err := w.Execute(context.Background(), json.RawMessage(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestWriteFileSandboxCheck(t *testing.T) {
	w := WriteFile{SandboxCheck: func(path string) bool { return false }}
	input, _ := json.Marshal(map[string]string{"path": "/forbidden/file.txt", "content": "data"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected sandbox error")
	}
}

func TestWriteFileSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "special.txt")

	w := WriteFile{}
	content := "hello\nworld\ttab\reol\r\nunicode: 你好世界 🌍"
	input, _ := json.Marshal(map[string]string{"path": fp, "content": content})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != content {
		t.Errorf("content mismatch")
	}
}
