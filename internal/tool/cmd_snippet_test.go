package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newCmdSnippetTool(t *testing.T) (*CmdSnippetTool, string) {
	t.Helper()
	dir := t.TempDir()
	return &CmdSnippetTool{WorkingDir: dir}, dir
}

func TestCmdSnippet_SaveAndGet(t *testing.T) {
	tool, _ := newCmdSnippetTool(t)

	// Save
	input, _ := json.Marshal(map[string]string{
		"action":            "save",
		"name":              "build-go",
		"command":           "go build -tags goolm ./...",
		"description_field": "Build the Go project with goolm tag",
		"description":       "test activity",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("save returned error: %s", result.Content)
	}

	// Get
	input, _ = json.Marshal(map[string]string{
		"action":      "get",
		"name":        "build-go",
		"description": "test activity",
	})
	result, err = tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("get returned error: %s", result.Content)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestCmdSnippet_ListEmpty(t *testing.T) {
	tool, _ := newCmdSnippetTool(t)

	input, _ := json.Marshal(map[string]string{
		"action":      "list",
		"description": "test",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("list should not error on empty store: %s", result.Content)
	}
}

func TestCmdSnippet_SaveUpdate(t *testing.T) {
	tool, _ := newCmdSnippetTool(t)

	// Save first version
	input, _ := json.Marshal(map[string]string{
		"action":      "save",
		"name":        "test-cmd",
		"command":     "echo hello",
		"description": "test",
	})
	_, _ = tool.Execute(context.Background(), input)

	// Save updated version (same name)
	input, _ = json.Marshal(map[string]string{
		"action":            "save",
		"name":              "test-cmd",
		"command":           "echo world",
		"description_field": "updated description",
		"description":       "test",
	})
	result, _ := tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("update failed: %s", result.Content)
	}

	// Verify update
	input, _ = json.Marshal(map[string]string{
		"action":      "get",
		"name":        "test-cmd",
		"description": "test",
	})
	result, _ = tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("get after update failed: %s", result.Content)
	}
}

func TestCmdSnippet_Delete(t *testing.T) {
	tool, _ := newCmdSnippetTool(t)

	// Save
	input, _ := json.Marshal(map[string]string{
		"action":      "save",
		"name":        "to-delete",
		"command":     "echo bye",
		"description": "test",
	})
	_, _ = tool.Execute(context.Background(), input)

	// Delete
	input, _ = json.Marshal(map[string]string{
		"action":      "delete",
		"name":        "to-delete",
		"description": "test",
	})
	result, _ := tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("delete failed: %s", result.Content)
	}

	// Verify deleted
	input, _ = json.Marshal(map[string]string{
		"action":      "get",
		"name":        "to-delete",
		"description": "test",
	})
	result, _ = tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Fatal("expected error for deleted snippet")
	}
}

func TestCmdSnippet_Search(t *testing.T) {
	tool, _ := newCmdSnippetTool(t)

	// Save multiple snippets
	snippets := []struct {
		name, command, desc string
		tags                []string
	}{
		{"build-go", "go build ./...", "Build Go project", []string{"build"}},
		{"test-go", "go test ./...", "Run Go tests", []string{"test"}},
		{"deploy-k8s", "kubectl apply -f deploy.yaml", "Deploy to k8s", []string{"deploy"}},
	}
	for _, s := range snippets {
		input, _ := json.Marshal(map[string]interface{}{
			"action":            "save",
			"name":              s.name,
			"command":           s.command,
			"description_field": s.desc,
			"tags":              s.tags,
			"description":       "test",
		})
		_, _ = tool.Execute(context.Background(), input)
	}

	// Search by name keyword
	input, _ := json.Marshal(map[string]string{
		"action":      "search",
		"query":       "build",
		"description": "test",
	})
	result, _ := tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("search failed: %s", result.Content)
	}
	if result.Content == "" {
		t.Fatal("expected search results")
	}
	// Should match build-go
	if !strings.Contains(result.Content, "build-go") {
		t.Errorf("search should find 'build-go', got: %s", result.Content)
	}
	// Should NOT match deploy-k8s
	if strings.Contains(result.Content, "deploy-k8s") {
		t.Errorf("search should not find 'deploy-k8s', got: %s", result.Content)
	}
}

func TestCmdSnippet_PersistenceAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	tool1 := &CmdSnippetTool{WorkingDir: dir}

	// Save with first instance
	input, _ := json.Marshal(map[string]string{
		"action":      "save",
		"name":        "persisted-cmd",
		"command":     "make verify",
		"description": "test",
	})
	_, _ = tool1.Execute(context.Background(), input)

	// Create a new instance pointing to same dir
	tool2 := &CmdSnippetTool{WorkingDir: dir}
	input, _ = json.Marshal(map[string]string{
		"action":      "get",
		"name":        "persisted-cmd",
		"description": "test",
	})
	result, _ := tool2.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("persistence failed: %s", result.Content)
	}

	// Verify file exists
	path := filepath.Join(dir, ".ggcode", cmdSnippetFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected snippet file at %s: %v", path, err)
	}
}

func TestCmdSnippet_Validation(t *testing.T) {
	tool, _ := newCmdSnippetTool(t)

	tests := []struct {
		name  string
		input map[string]interface{}
	}{
		{"missing name", map[string]interface{}{"action": "save", "command": "echo"}},
		{"missing command", map[string]interface{}{"action": "save", "name": "test"}},
		{"unknown action", map[string]interface{}{"action": "foo", "description": "test"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(tt.input)
			result, _ := tool.Execute(context.Background(), input)
			if !result.IsError {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestCmdSnippet_Clone(t *testing.T) {
	original := &CmdSnippetTool{WorkingDir: "/tmp/test"}
	cloned := original.Clone()
	cl, ok := cloned.(*CmdSnippetTool)
	if !ok {
		t.Fatal("Clone should return *CmdSnippetTool")
	}
	if cl.WorkingDir != original.WorkingDir {
		t.Error("Clone should preserve WorkingDir")
	}
}

func TestCmdSnippet_TagsSearch(t *testing.T) {
	tool, _ := newCmdSnippetTool(t)

	input, _ := json.Marshal(map[string]interface{}{
		"action":            "save",
		"name":              "my-build",
		"command":           "make build",
		"description_field": "Build project",
		"tags":              []string{"build", "important"},
		"description":       "test",
	})
	_, _ = tool.Execute(context.Background(), input)

	// Search by tag
	input, _ = json.Marshal(map[string]string{
		"action":      "search",
		"query":       "important",
		"description": "test",
	})
	result, _ := tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("tag search failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "my-build") {
		t.Errorf("tag search should find 'my-build': %s", result.Content)
	}
}
