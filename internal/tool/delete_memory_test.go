package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

func TestDeleteMemoryTool_DefaultProjectScope(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)
	if pm == nil {
		t.Fatal("expected non-nil project memory")
	}

	// Save a memory first
	am.SaveMemory("test-pattern", "some content")
	pm.SaveMemory("project-key", "project content")

	tol := NewDeleteMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key": "project-key",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	// Verify project file was deleted
	if _, err := os.Stat(filepath.Join(pm.Dir(), "project-key.md")); !os.IsNotExist(err) {
		t.Fatal("project file should be deleted")
	}
}

func TestDeleteMemoryTool_GlobalScope(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)

	am.SaveMemory("global-key", "global content")

	tol := NewDeleteMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key":   "global-key",
		"scope": "global",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	if _, err := os.Stat(filepath.Join(am.Dir(), "global-key.md")); !os.IsNotExist(err) {
		t.Fatal("global file should be deleted")
	}
}

func TestDeleteMemoryTool_NotFound(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)

	tol := NewDeleteMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key": "nonexistent",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestDeleteMemoryTool_InvalidScope(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)

	tol := NewDeleteMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key":   "test",
		"scope": "invalid",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid scope")
	}
}

func TestDeleteMemoryTool_AfterSaveCallback(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)

	pm.SaveMemory("callback-test", "content")

	callbackCalled := false
	tol := NewDeleteMemoryTool(am, pm)
	tol.SetAfterSave(func() { callbackCalled = true })

	input, _ := json.Marshal(map[string]string{
		"key": "callback-test",
	})

	result, _ := tol.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !callbackCalled {
		t.Error("afterSave callback should have been called")
	}
}

func TestDeleteMemoryTool_NoProjectRoot(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	// HOME dir - NewProjectAutoMemory returns nil
	pm := memory.NewProjectAutoMemory(os.Getenv("HOME"))

	tol := NewDeleteMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key": "test",
	})

	result, _ := tol.Execute(context.Background(), input)
	if !result.IsError {
		t.Fatal("expected error when project memory is nil")
	}
}

func TestDeleteMemoryTool_EmptyKey(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)

	tol := NewDeleteMemoryTool(am, pm)

	input, _ := json.Marshal(map[string]string{
		"key": "",
	})

	result, _ := tol.Execute(context.Background(), input)
	if !result.IsError {
		t.Fatal("expected error for empty key")
	}
}
