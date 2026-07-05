package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestEditFile_DiagnoseMatchFailure_NearestLines(t *testing.T) {
	// When old_text doesn't match exactly, the diagnostic should find the
	// nearest matching lines in the file and include them with line numbers.
	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	oldText := "\tfmt.Println(\"world\")" // close but different argument

	hint := diagnoseMatchFailure(content, oldText)
	// Should find the nearest line (fmt.Println with "hello") on line 6
	if !strings.Contains(hint, "fmt.Println") {
		t.Errorf("expected diagnostic to find nearest matching line containing fmt.Println, got: %s", hint)
	}
	// Should include line number 6 (where fmt.Println("hello") is)
	if !strings.Contains(hint, "\n\t6\t") && !strings.Contains(hint, "  6\t") {
		t.Errorf("expected diagnostic to include line number 6, got: %s", hint)
	}
}

func TestEditFile_DiagnoseMatchFailure_NoMatchInFile(t *testing.T) {
	// When old_text has no similar lines in the file, the diagnostic should
	// not crash and should return the generic hint.
	content := "package main\n\nfunc main() {}\n"
	oldText := "completely different and unrelated text"

	hint := diagnoseMatchFailure(content, oldText)
	if hint == "" {
		t.Error("expected non-empty diagnostic")
	}
}

func TestFindNearestLines(t *testing.T) {
	fileLines := []string{
		"package main",
		"",
		"import \"fmt\"",
		"",
		"func processData(data []byte) error {",
		"\tresult := transform(data)",
		"\treturn nil",
		"}",
	}

	tests := []struct {
		name    string
		oldText string
		wantSub string // substring expected in a matched line
	}{
		{"exact first line match", "func processData(data []byte) error {", "func processData"},
		{"close match", "func processData(items []byte) error {", "func processData"},
		{"token overlap", "\tresult := transform(data)", "transform"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := findNearestLines(fileLines, tc.oldText, 3)
			if len(result) == 0 {
				t.Fatalf("expected at least one match for %q", tc.oldText)
			}
			found := false
			for _, nl := range result {
				if strings.Contains(nl.text, tc.wantSub) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected a match containing %q, got: %+v", tc.wantSub, result)
			}
		})
	}

	// No match case
	result := findNearestLines(fileLines, "zzzzzz nonexist", 3)
	if len(result) != 0 {
		t.Errorf("expected no matches for unrelated text, got %d", len(result))
	}
}

func TestTokenize(t *testing.T) {
	tokens := tokenize("Hello_World 123 foo-bar")
	expected := map[string]struct{}{
		"hello_world": {},
		"123":         {},
		"foo":         {},
		"bar":         {},
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d: %v", len(expected), len(tokens), tokens)
	}
	for tok := range expected {
		if _, ok := tokens[tok]; !ok {
			t.Errorf("missing token %q", tok)
		}
	}
}

func TestJaccardSimilarity(t *testing.T) {
	a := tokenize("func processData")
	b := tokenize("func processData data")
	// a = {func, processdata}, b = {func, processdata, data}
	// intersection = 2, union = 3, jaccard = 0.667
	sim := jaccardSimilarity(a, b)
	if sim < 0.6 || sim > 0.7 {
		t.Errorf("expected ~0.667, got %f", sim)
	}
	// Identical sets → 1.0
	if s := jaccardSimilarity(a, a); s != 1.0 {
		t.Errorf("expected 1.0 for identical sets, got %f", s)
	}
	// Disjoint sets → 0
	if s := jaccardSimilarity(a, tokenize("zzzzz")); s != 0 {
		t.Errorf("expected 0 for disjoint sets, got %f", s)
	}
}
