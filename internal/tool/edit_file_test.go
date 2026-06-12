package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEditFileSchemaWarnsOldTextUniqueness(t *testing.T) {
	params := string(EditFile{}.Parameters())
	for _, want := range []string{"line-number prefixes", "byte-for-byte", "unique in the file"} {
		if !containsAny(params, want) {
			t.Fatalf("edit_file schema should mention %q, got %s", want, params)
		}
	}
}

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

func TestEditFile_TabFileSpaceOldText_AutoNormalize(t *testing.T) {
	// File uses tabs, but the LLM sends old_text with spaces (the #1 failure case).
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	// File content with tab indentation
	os.WriteFile(fp, []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)

	ef := EditFile{}
	// LLM sends 4-space indentation instead of tab
	input := json.RawMessage(`{
		"file_path": "` + fp + `",
		"old_text": "    fmt.Println(\"hello\")",
		"new_text": "    fmt.Println(\"world\")"
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected auto-normalization to succeed, got error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	// Result should preserve the file's tab indentation
	expected := "package main\n\nfunc main() {\n\tfmt.Println(\"world\")\n}\n"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestEditFile_TabFileSpaceOldText_Multiline(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	// File with nested tab indentation
	os.WriteFile(fp, []byte("package main\n\nfunc main() {\n\tif true {\n\t\tfmt.Println(\"hello\")\n\t}\n}\n"), 0644)

	ef := EditFile{}
	// LLM sends spaces for both tab levels
	input := json.RawMessage(`{
		"file_path": "` + fp + `",
		"old_text": "    if true {\n        fmt.Println(\"hello\")\n    }",
		"new_text": "    if false {\n        fmt.Println(\"bye\")\n    }"
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected auto-normalization to succeed, got error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	// Result should preserve tabs in the file
	expected := "package main\n\nfunc main() {\n\tif false {\n\t\tfmt.Println(\"bye\")\n\t}\n}\n"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestEditFile_SpaceFileTabOldText_AutoNormalize(t *testing.T) {
	// File uses spaces, but the LLM sends old_text with tabs.
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.yaml")
	os.WriteFile(fp, []byte("server:\n  port: 8080\n  host: localhost\n"), 0644)

	ef := EditFile{}
	// LLM sends tab indentation
	input := json.RawMessage(`{
		"file_path": "` + fp + `",
		"old_text": "\tport: 8080",
		"new_text": "\tport: 9090"
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected auto-normalization to succeed, got error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	expected := "server:\n  port: 9090\n  host: localhost\n"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestNormalizeIndentation_TabToFileSpaces(t *testing.T) {
	// Verify the normalization helper directly
	fileContent := "server:\n  port: 8080\n"
	text := "\tport: 8080"
	result := normalizeIndentation(fileContent, text)
	if result != "  port: 8080" {
		t.Errorf("expected 2-space indent, got %q", result)
	}
}

func TestNormalizeIndentation_SpacesToTabs(t *testing.T) {
	fileContent := "package main\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	text := "    fmt.Println(\"hi\")"
	result := normalizeIndentation(fileContent, text)
	if result != "\t"+"fmt.Println(\"hi\")" {
		// Should convert 4 spaces to one tab
		t.Errorf("expected tab indent, got %q", result)
	}
}
