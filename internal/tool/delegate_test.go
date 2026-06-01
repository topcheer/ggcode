package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// mockACPAgentRegistry implements ACPAgentRegistry for testing.
type mockACPAgentRegistry struct {
	agents    map[string]agentEntry
	promptErr error
}

type agentEntry struct {
	title       string
	description string
}

type mockACPAgentClient struct {
	result *ACPPromptResult
	err    error
}

func (m *mockACPAgentRegistry) Available() []string {
	names := make([]string, 0, len(m.agents))
	for n := range m.agents {
		names = append(names, n)
	}
	return names
}

func (m *mockACPAgentRegistry) AgentInfo(name string) (string, string, bool) {
	e, ok := m.agents[name]
	if !ok {
		return "", "", false
	}
	return e.title, e.description, true
}

func (m *mockACPAgentRegistry) SetWorkingDir(string) {}

func (m *mockACPAgentRegistry) Get(ctx context.Context, name string) (ACPAgentClient, error) {
	if _, ok := m.agents[name]; !ok {
		return nil, errAgentNotFound{name: name}
	}
	return &mockACPAgentClient{
		result: &ACPPromptResult{Text: "mock response from " + name, StopReason: "end_turn"},
		err:    m.promptErr,
	}, nil
}

func (m *mockACPAgentClient) Prompt(ctx context.Context, prompt string) (*ACPPromptResult, error) {
	return m.result, m.err
}

// errAgentNotFound for test purposes.
type errAgentNotFound struct{ name string }

func (e errAgentNotFound) Error() string { return "agent not found: " + e.name }

func TestDelegateToolName(t *testing.T) {
	tool := DelegateTool{Manager: &mockACPAgentRegistry{}}
	if tool.Name() != "delegate" {
		t.Errorf("expected name = %q, got %q", "delegate", tool.Name())
	}
}

func TestDelegateToolDescription(t *testing.T) {
	mgr := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"copilot": {"GitHub Copilot", "AI assistant"},
			"droid":   {"Droid", "Coding agent"},
		},
	}
	tool := DelegateTool{Manager: mgr}
	desc := tool.Description()
	if desc == "" {
		t.Error("expected non-empty description")
	}
	if !strings.Contains(desc, "copilot") {
		t.Error("description should mention copilot")
	}
	if !strings.Contains(desc, "droid") {
		t.Error("description should mention droid")
	}
}

func TestDelegateToolParameters(t *testing.T) {
	mgr := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"copilot": {"GitHub Copilot", "AI assistant"},
		},
	}
	tool := DelegateTool{Manager: mgr}
	params := tool.Parameters()

	var schema map[string]interface{}
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatalf("failed to parse parameters JSON: %v", err)
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'properties' in schema")
	}
	if _, ok := props["agent"]; !ok {
		t.Error("expected 'agent' property")
	}
	if _, ok := props["prompt"]; !ok {
		t.Error("expected 'prompt' property")
	}

	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatal("expected 'required' array")
	}
	if len(required) != 2 {
		t.Errorf("expected 2 required fields, got %d", len(required))
	}
}

func TestDelegateToolExecute(t *testing.T) {
	mgr := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"copilot": {"GitHub Copilot", "AI assistant"},
		},
	}
	tool := DelegateTool{Manager: mgr}

	input, _ := json.Marshal(map[string]string{
		"agent":  "copilot",
		"prompt": "analyze this file",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "mock response from copilot") {
		t.Errorf("expected response to contain agent output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "GitHub Copilot") {
		t.Errorf("expected response to contain agent title, got: %s", result.Content)
	}
}

func TestDelegateToolExecuteAgentNotFound(t *testing.T) {
	mgr := &mockACPAgentRegistry{agents: map[string]agentEntry{}}
	tool := DelegateTool{Manager: mgr}

	input, _ := json.Marshal(map[string]string{
		"agent":  "nonexistent",
		"prompt": "test",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for nonexistent agent")
	}
}

func TestDelegateToolExecuteMissingParams(t *testing.T) {
	mgr := &mockACPAgentRegistry{agents: map[string]agentEntry{}}
	tool := DelegateTool{Manager: mgr}

	// Missing agent
	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing agent")
	}

	// Missing prompt
	input, _ = json.Marshal(map[string]string{"agent": "copilot"})
	result, err = tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing prompt")
	}
}

func TestDelegateToolEmptyAgents(t *testing.T) {
	mgr := &mockACPAgentRegistry{agents: map[string]agentEntry{}}
	tool := DelegateTool{Manager: mgr}

	desc := tool.Description()
	if !strings.Contains(desc, "No agents currently available") {
		t.Errorf("expected 'no agents' message when empty")
	}
}
