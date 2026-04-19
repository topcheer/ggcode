package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEditFile_Basic(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)

	ef := EditFile{}
	input := json.RawMessage(`{
		"file_path": "` + fp + `",
		"old_text": "hello",
		"new_text": "world"
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "package main\n\nfunc main() {\n\tfmt.Println(\"world\")\n}\n" {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

func TestEditFile_NotFound(t *testing.T) {
	ef := EditFile{}
	input := json.RawMessage(`{
		"file_path": "/nonexistent/path/file.go",
		"old_text": "hello",
		"new_text": "world"
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent file")
	}
}

func TestEditFile_OldTextNotFound(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte("hello world\n"), 0644)

	ef := EditFile{}
	input := json.RawMessage(`{
		"file_path": "` + fp + `",
		"old_text": "goodbye",
		"new_text": "farewell"
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when old_text not found")
	}
}

func TestEditFile_InvalidInput(t *testing.T) {
	ef := EditFile{}
	result, err := ef.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing required fields")
	}
}

func TestEditFile_MultilineReplace(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "line1\nline2\nline3\n"
	os.WriteFile(fp, []byte(content), 0644)

	ef := EditFile{}
	input := json.RawMessage(`{
		"file_path": "` + fp + `",
		"old_text": "line1\nline2",
		"new_text": "replaced1\nreplaced2"
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	expected := "replaced1\nreplaced2\nline3\n"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestEditFile_EmptyNewText(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte("keep this\nremove this\nkeep that\n"), 0644)

	ef := EditFile{}
	input := json.RawMessage(`{
		"file_path": "` + fp + `",
		"old_text": "remove this\n",
		"new_text": ""
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	expected := "keep this\nkeep that\n"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}
