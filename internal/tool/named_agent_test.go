package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
)

// setupDeleteTestStore creates a TemplateStore with an isolated HOME directory
// so tests don't interfere with real subagent templates.
func setupDeleteTestStore(t *testing.T) (*subagent.TemplateStore, string) {
	t.Helper()
	homeDir := t.TempDir()
	// Setenv must happen BEFORE NewTemplateStore, since NewTemplateStore
	// calls config.HomeDir() at construction time.
	t.Setenv("HOME", homeDir)

	// Use a unique workspace path to avoid collisions.
	workspace := filepath.Join(homeDir, "myproject")
	store := subagent.NewTemplateStore(workspace)

	hashDir := store.TemplateDir()
	if err := os.MkdirAll(hashDir, 0755); err != nil {
		t.Fatalf("mkdir template dir: %v", err)
	}
	return store, hashDir
}

func createTestTemplate(t *testing.T, store *subagent.TemplateStore, name, desc, prompt string) {
	t.Helper()
	tmpl := subagent.NamedAgentTemplate{
		Name:         name,
		Description:  desc,
		SystemPrompt: prompt,
	}
	if err := store.Save(tmpl); err != nil {
		t.Fatalf("save template: %v", err)
	}
}

func TestDeleteNamedAgentTool_Success(t *testing.T) {
	store, hashDir := setupDeleteTestStore(t)
	createTestTemplate(t, store, "code-reviewer", "Code reviewer", "You are a code reviewer.")

	// Verify file exists.
	path := filepath.Join(hashDir, "code-reviewer.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("template file should exist: %v", err)
	}

	tool := DeleteNamedAgentTool{Store: store}
	input, _ := json.Marshal(map[string]string{"name": "code-reviewer"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Errorf("expected success, got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "deleted successfully") {
		t.Errorf("unexpected result: %s", result.Content)
	}

	// Verify file is gone.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("template file should be deleted")
	}

	// Verify Load fails.
	if _, err := store.Load("code-reviewer"); err == nil {
		t.Errorf("Load should fail after deletion")
	}
}

func TestDeleteNamedAgentTool_NotFound(t *testing.T) {
	store, _ := setupDeleteTestStore(t)

	tool := DeleteNamedAgentTool{Store: store}
	input, _ := json.Marshal(map[string]string{"name": "non-existent"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error for non-existent template")
	}
	if !strings.Contains(result.Content, "not found") {
		t.Errorf("expected 'not found' in error message, got: %s", result.Content)
	}
}

func TestDeleteNamedAgentTool_EmptyName(t *testing.T) {
	store, _ := setupDeleteTestStore(t)

	tool := DeleteNamedAgentTool{Store: store}
	input, _ := json.Marshal(map[string]string{"name": ""})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error for empty name")
	}
	if !strings.Contains(result.Content, "name is required") {
		t.Errorf("expected 'name is required' in error, got: %s", result.Content)
	}
}

func TestDeleteNamedAgentTool_WhitespaceName(t *testing.T) {
	store, _ := setupDeleteTestStore(t)

	tool := DeleteNamedAgentTool{Store: store}
	input, _ := json.Marshal(map[string]string{"name": "   "})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error for whitespace-only name")
	}
}

func TestDeleteNamedAgentTool_NilStore(t *testing.T) {
	tool := DeleteNamedAgentTool{Store: nil}
	input, _ := json.Marshal(map[string]string{"name": "anything"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error for nil store")
	}
}

func TestDeleteNamedAgentTool_InvalidInput(t *testing.T) {
	store, _ := setupDeleteTestStore(t)

	tool := DeleteNamedAgentTool{Store: store}
	result, err := tool.Execute(context.Background(), json.RawMessage(`not json`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error for invalid JSON input")
	}
}

func TestDeleteNamedAgentTool_Metadata(t *testing.T) {
	store, _ := setupDeleteTestStore(t)
	tool := DeleteNamedAgentTool{Store: store}

	if tool.Name() != "delete_namedagent" {
		t.Errorf("expected name 'delete_namedagent', got %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}

	params := tool.Parameters()
	var schema struct {
		Type       string `json:"type"`
		Properties struct {
			Name struct {
				Type string `json:"type"`
			} `json:"name"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatalf("parse parameters: %v", err)
	}
	if schema.Properties.Name.Type != "string" {
		t.Errorf("expected name property type 'string', got %q", schema.Properties.Name.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "name" {
		t.Errorf("expected required [name], got %v", schema.Required)
	}
}

func TestDeleteNamedAgentTool_DeleteOneOfMultiple(t *testing.T) {
	store, hashDir := setupDeleteTestStore(t)
	createTestTemplate(t, store, "code-reviewer", "Code reviewer", "Review code.")
	createTestTemplate(t, store, "test-writer", "Test writer", "Write tests.")

	tool := DeleteNamedAgentTool{Store: store}
	input, _ := json.Marshal(map[string]string{"name": "code-reviewer"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Errorf("expected success: %s", result.Content)
	}

	// code-reviewer should be gone.
	if _, err := os.Stat(filepath.Join(hashDir, "code-reviewer.json")); !os.IsNotExist(err) {
		t.Errorf("code-reviewer should be deleted")
	}

	// test-writer should still exist.
	if _, err := os.Stat(filepath.Join(hashDir, "test-writer.json")); err != nil {
		t.Errorf("test-writer should still exist: %v", err)
	}

	// List should only return test-writer.
	templates, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(templates) != 1 {
		t.Errorf("expected 1 template, got %d", len(templates))
	}
	if templates[0].Name != "test-writer" {
		t.Errorf("expected 'test-writer', got %q", templates[0].Name)
	}
}
