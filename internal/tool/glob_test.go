package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGlobBasicPattern(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("hello"), 0o644)

	g := Glob{}
	input, _ := json.Marshal(map[string]string{"pattern": "*.go", "directory": dir})
	result, err := g.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAll(result.Content, "a.go", "b.go") {
		t.Errorf("expected a.go and b.go in output: %s", result.Content)
	}
	if containsStr(result.Content, "c.txt") {
		t.Error("c.txt should not match *.go")
	}
}

func TestGlobNoMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)

	g := Glob{}
	input, _ := json.Marshal(map[string]string{"pattern": "*.go", "directory": dir})
	result, err := g.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "No files matched") {
		t.Errorf("expected no match message: %s", result.Content)
	}
}

func TestGlobInvalidInput(t *testing.T) {
	g := Glob{}
	result, err := g.Execute(context.Background(), json.RawMessage(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestGlobMissingPattern(t *testing.T) {
	g := Glob{}
	// Empty pattern → filepath.Glob returns error or empty result
	result, err := g.Execute(context.Background(), json.RawMessage(`{"pattern":"","directory":"/tmp"}`))
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic
	t.Logf("result: %s", result.Content)
}

func TestGlobSandboxCheck(t *testing.T) {
	g := Glob{SandboxCheck: func(path string) bool { return false }}
	input, _ := json.Marshal(map[string]string{"pattern": "*.go", "directory": "/forbidden"})
	result, err := g.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected sandbox error")
	}
}

func TestGlobNonexistentDirectory(t *testing.T) {
	g := Glob{}
	input, _ := json.Marshal(map[string]string{"pattern": "*.go", "directory": "/nonexistent/path"})
	result, err := g.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic, may return error or no matches
	t.Logf("result: %s", result.Content)
}

func TestGlobRecursivePattern(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "nested.go"), []byte("package sub"), 0o644)

	g := Glob{}
	input, _ := json.Marshal(map[string]string{"pattern": "**/*.go", "directory": dir})
	result, err := g.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "nested.go") {
		t.Errorf("expected nested.go in output: %s", result.Content)
	}
}
