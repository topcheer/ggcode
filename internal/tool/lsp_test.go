package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPSymbolsToolWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/test\n\ngo 1.26.1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	path := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Add(a int, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.go) error = %v", err)
	}
	tool := NewLSPTools(workspace, nil, nil)[3]
	input, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	if !strings.Contains(result.Content, "Add") {
		t.Fatalf("expected Add symbol in tool output, got %q", result.Content)
	}
}

func TestLSPWorkspaceSymbolsToolWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/test\n\ngo 1.26.1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	path := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Add(a int, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.go) error = %v", err)
	}
	tool := NewLSPTools(workspace, nil, nil)[4]
	input, err := json.Marshal(map[string]string{"query": "Add"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	if !strings.Contains(result.Content, "Add") {
		t.Fatalf("expected Add symbol in workspace symbol output, got %q", result.Content)
	}
}

func TestLSPRenameToolWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/test\n\ngo 1.26.1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	path := filepath.Join(workspace, "sample.go")
	source := "package sample\n\nfunc Add(a int, b int) int { return a + b }\n\nfunc Use() int { return Add(1, 2) }\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.go) error = %v", err)
	}
	allow := func(candidate string) bool { return strings.HasPrefix(candidate, workspace) }
	tool := NewLSPTools(workspace, allow, allow)[7]
	input, err := json.Marshal(map[string]any{"path": path, "line": 3, "character": 6, "new_name": "Sum"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(sample.go) error = %v", err)
	}
	if !strings.Contains(string(updated), "func Sum") || !strings.Contains(string(updated), "return Sum(1, 2)") {
		t.Fatalf("expected rename to update file, got %q", string(updated))
	}
}
