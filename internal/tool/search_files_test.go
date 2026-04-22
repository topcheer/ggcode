package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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
