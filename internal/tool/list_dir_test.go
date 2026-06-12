package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestListDirBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package main"), 0o644)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)

	ld := ListDir{}
	input, _ := json.Marshal(map[string]string{"path": dir})
	result, err := ld.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "a.txt") || !containsStr(result.Content, "b.go") {
		t.Errorf("expected file names in output: %s", result.Content)
	}
	if !containsStr(result.Content, "subdir") {
		t.Errorf("expected subdir in output: %s", result.Content)
	}
}

func TestListDirEmpty(t *testing.T) {
	dir := t.TempDir()

	ld := ListDir{}
	input, _ := json.Marshal(map[string]string{"path": dir})
	result, err := ld.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Empty dir should return empty result
	t.Logf("empty dir result: %s", result.Content)
}

func TestListDirNotFound(t *testing.T) {
	ld := ListDir{}
	input, _ := json.Marshal(map[string]string{"path": "/nonexistent/dir"})
	result, err := ld.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent dir")
	}
}

func TestListDirInvalidInput(t *testing.T) {
	ld := ListDir{}
	result, err := ld.Execute(context.Background(), json.RawMessage(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestListDirDefaultPath(t *testing.T) {
	ld := ListDir{}
	// Empty path defaults to "."
	input, _ := json.Marshal(map[string]string{"path": ""})
	result, err := ld.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic
	t.Logf("default path result length: %d", len(result.Content))
}

func TestListDirSandboxCheck(t *testing.T) {
	ld := ListDir{SandboxCheck: func(path string) bool { return false }}
	input, _ := json.Marshal(map[string]string{"path": "/forbidden"})
	result, err := ld.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected sandbox error")
	}
}
