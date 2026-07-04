package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

// createTestProjectDir creates a temp dir with .git so NewProjectAutoMemory recognizes it.
func createTestProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSaveMemoryTool_DefaultProjectScope(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)
	if pm == nil {
		t.Fatal("expected non-nil project memory")
	}

	tol := NewSaveMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key":     "test-pattern",
		"content": "Always use errors.Is()",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	// Default scope is project — verify project file was created
	data, err := os.ReadFile(filepath.Join(pm.Dir(), "test-pattern.md"))
	if err != nil {
		t.Fatalf("project file not found: %v", err)
	}
	if string(data) != "Always use errors.Is()" {
		t.Errorf("wrong content: %q", string(data))
	}
}

func TestSaveMemoryTool_GlobalScope(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)

	tol := NewSaveMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key":     "global-pattern",
		"content": "Never hardcode secrets",
		"scope":   "global",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, err := os.ReadFile(filepath.Join(am.Dir(), "global-pattern.md"))
	if err != nil {
		t.Fatalf("global file not found: %v", err)
	}
	if string(data) != "Never hardcode secrets" {
		t.Errorf("wrong content: %q", string(data))
	}
}

func TestSaveMemoryTool_ExplicitProjectScope(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)

	tol := NewSaveMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key":     "project-build",
		"content": "Use go build -tags goolm",
		"scope":   "project",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, err := os.ReadFile(filepath.Join(pm.Dir(), "project-build.md"))
	if err != nil {
		t.Fatalf("project file not found: %v", err)
	}
	if string(data) != "Use go build -tags goolm" {
		t.Errorf("wrong content: %q", string(data))
	}
}

func TestSaveMemoryTool_InvalidScope(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)

	tol := NewSaveMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key":     "bad",
		"content": "test",
		"scope":   "invalid",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid scope")
	}
}

func TestSaveMemoryTool_NoProjectRoot(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	// HOME dir — NewProjectAutoMemory returns nil
	pm := memory.NewProjectAutoMemory(os.Getenv("HOME"))
	if pm != nil {
		t.Fatal("expected nil project memory for HOME dir")
	}

	tol := NewSaveMemoryTool(am, pm)

	// Default scope is project — should fail gracefully
	input, _ := json.Marshal(map[string]string{
		"key":     "test",
		"content": "should fail",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when project memory is nil")
	}

	// Global scope should still work
	input2, _ := json.Marshal(map[string]string{
		"key":     "global-ok",
		"content": "works fine",
		"scope":   "global",
	})

	result2, err := tol.Execute(context.Background(), input2)
	if err != nil {
		t.Fatalf("Execute global: %v", err)
	}
	if result2.IsError {
		t.Fatalf("global should work: %s", result2.Content)
	}
}

func TestSaveMemoryTool_NoGitStillWorks(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	// No .git — should still get project memory (just a plain directory)
	projectDir := t.TempDir()
	pm := memory.NewProjectAutoMemory(projectDir)
	if pm == nil {
		t.Fatal("expected non-nil project memory even without .git")
	}

	tol := NewSaveMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key":     "no-git-pattern",
		"content": "Still works without git",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, err := os.ReadFile(filepath.Join(pm.Dir(), "no-git-pattern.md"))
	if err != nil {
		t.Fatalf("project file not found: %v", err)
	}
	if string(data) != "Still works without git" {
		t.Errorf("wrong content: %q", string(data))
	}
}

func TestSaveMemoryTool_CallsAfterSaveHook(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)
	if pm == nil {
		t.Fatal("expected non-nil project memory")
	}

	tol := NewSaveMemoryTool(am, pm)
	called := 0
	tol.SetAfterSave(func() {
		called++
	})

	input, _ := json.Marshal(map[string]string{
		"key":     "hook-test",
		"content": "refresh prompt after save",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if called != 1 {
		t.Fatalf("expected after-save hook once, got %d", called)
	}
}
