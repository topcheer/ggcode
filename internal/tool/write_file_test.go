package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileSchemaWarnsOverwriteAndAbsolutePath(t *testing.T) {
	params := string(WriteFile{}.Parameters())
	for _, want := range []string{"Prefer an absolute path", "fully replaced", "use edit_file for targeted changes"} {
		if !containsAny(params, want) {
			t.Fatalf("write_file schema should mention %q, got %s", want, params)
		}
	}
}

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
	// write_file returns an error when overwriting an existing non-empty file,
	// prompting the agent to use edit_file instead or retry if intentional.
	if !result.IsError {
		t.Fatalf("expected overwrite warning error, got success: %s", result.Content)
	}

	// File should not be modified when overwrite protection triggers.
	data, _ := os.ReadFile(fp)
	if string(data) != "old content" {
		t.Errorf("file should not be modified during overwrite warning, got %q", string(data))
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
