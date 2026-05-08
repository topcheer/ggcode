package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

func TestSaveMemoryTool(t *testing.T) {
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	pm := memory.NewProjectAutoMemory(am.Dir())
	defer os.RemoveAll(pm.Dir())

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
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	pm := memory.NewProjectAutoMemory(am.Dir())
	defer os.RemoveAll(pm.Dir())

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

func TestSaveMemoryTool_ProjectScope(t *testing.T) {
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	pm := memory.NewProjectAutoMemory(am.Dir())
	defer os.RemoveAll(pm.Dir())

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
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	pm := memory.NewProjectAutoMemory(am.Dir())
	defer os.RemoveAll(pm.Dir())

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
