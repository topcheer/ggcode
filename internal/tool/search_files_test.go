package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchFilesDescriptionPointsToGrepForAdvancedSearch(t *testing.T) {
	tool := SearchFiles{}
	for _, want := range []string{"Prefer grep", "context lines", "large-repository performance"} {
		if !containsStr(tool.Description(), want) {
			t.Fatalf("search_files description should mention %q, got %q", want, tool.Description())
		}
	}
	if !containsStr(string(tool.Parameters()), "use glob instead") {
		t.Fatalf("search_files schema should clarify path-only discovery, got %s", string(tool.Parameters()))
	}
}

func TestSearchFilesBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nfunc hello() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("no match here\n"), 0o644)

	s := SearchFiles{}
	input, _ := json.Marshal(map[string]interface{}{"pattern": "hello", "directory": dir})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "hello") {
		t.Errorf("expected hello match: %s", result.Content)
	}
}

func TestSearchFilesNoMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("nothing here\n"), 0o644)

	s := SearchFiles{}
	input, _ := json.Marshal(map[string]interface{}{"pattern": "nonexistent_pattern_xyz", "directory": dir})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if containsStr(result.Content, "0 matches") || containsStr(result.Content, "No matches") {
		return // ok
	}
	t.Logf("result: %s", result.Content)
}

func TestSearchFilesInvalidRegex(t *testing.T) {
	s := SearchFiles{}
	input, _ := json.Marshal(map[string]interface{}{"pattern": "[invalid", "directory": "."})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid regex")
	}
}

func TestSearchFilesInvalidInput(t *testing.T) {
	s := SearchFiles{}
	result, err := s.Execute(context.Background(), json.RawMessage(`bad json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestSearchFilesWithIncludePattern(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nfunc findme() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("findme\n"), 0o644)

	s := SearchFiles{}
	input, _ := json.Marshal(map[string]interface{}{
		"pattern":         "findme",
		"directory":       dir,
		"include_pattern": "*.go",
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "a.go") {
		t.Errorf("expected a.go match: %s", result.Content)
	}
}

func TestSearchFilesEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	s := SearchFiles{}
	input, _ := json.Marshal(map[string]interface{}{"pattern": "anything", "directory": dir})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic
	t.Logf("result: %s", result.Content)
}

func TestSearchFilesSandboxCheck(t *testing.T) {
	s := SearchFiles{SandboxCheck: func(path string) bool { return false }}
	input, _ := json.Marshal(map[string]interface{}{"pattern": "test", "directory": "/forbidden"})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected sandbox error")
	}
}

func TestSearchFilesMaxResults(t *testing.T) {
	dir := t.TempDir()
	// Create files with matches
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.txt", i)), []byte("matchline\n"), 0o644)
	}

	s := SearchFiles{}
	input, _ := json.Marshal(map[string]interface{}{
		"pattern":     "matchline",
		"directory":   dir,
		"max_results": 3,
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Should return limited results
	t.Logf("result: %s", result.Content)
}

// TestSearchFilesIncludePatternDeepPath1702: the git fast path treats
// include_pattern as a pathspec (src/*.go matches src/deep/f.go); the
// fallback used basename-only matching, so the SAME argument silently
// returned zero results when git was unavailable. The fallback now
// accepts the repo-relative path too. Run in a directory that is NOT a
// git repo to force the fallback path.
func TestSearchFilesIncludePatternDeepPath1702(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "src", "nested")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "f.go"), []byte("needleXYZ\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sanity: t.TempDir is outside any git repo work tree.
	s := SearchFiles{}
	input, _ := json.Marshal(map[string]interface{}{"pattern": "needleXYZ", "directory": dir, "include_pattern": "src/*.go", "max_results": 10})
	r, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Content, "no matches") || !strings.Contains(r.Content, "f.go") {
		t.Fatalf("fallback path must match deep pathspec forms, got: %s", r.Content)
	}
}
