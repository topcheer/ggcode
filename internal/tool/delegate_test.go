package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

// mockACPAgentRegistry implements ACPAgentRegistry for testing.
type mockACPAgentRegistry struct {
	agents       map[string]agentEntry
	promptErr    error
	promptResult *ACPPromptResult
	promptEvents []ACPPromptEvent
}

type agentEntry struct {
	title       string
	description string
}

type mockACPAgentClient struct {
	result *ACPPromptResult
	err    error
	events []ACPPromptEvent
	closed int
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
		result: defaultACPResult(m.promptResult, name),
		err:    m.promptErr,
		events: append([]ACPPromptEvent(nil), m.promptEvents...),
	}, nil
}

func defaultACPResult(result *ACPPromptResult, name string) *ACPPromptResult {
	if result != nil {
		return result
	}
	return &ACPPromptResult{Text: "mock response from " + name, StopReason: "end_turn"}
}

func (m *mockACPAgentClient) Prompt(ctx context.Context, prompt string) (*ACPPromptResult, error) {
	return m.result, m.err
}

func (m *mockACPAgentClient) PromptStream(
	ctx context.Context,
	prompt string,
	onEvent func(ACPPromptEvent),
) (*ACPPromptResult, error) {
	for _, event := range m.events {
		onEvent(event)
	}
	return m.result, m.err
}

func (m *mockACPAgentClient) Close() error {
	m.closed++
	return nil
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

func TestDelegateToolExecuteAsync(t *testing.T) {
	mgr := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"copilot": {"GitHub Copilot", "AI assistant"},
		},
		promptResult: &ACPPromptResult{
			Text: "## Heading\n\nFinal markdown",
			ToolCalls: []ACPToolCallSummary{
				{Name: "Read file", Title: "Read file", Status: "completed"},
			},
		},
		promptEvents: []ACPPromptEvent{
			{Type: ACPPromptEventText, Text: "## Heading\n\n"},
			{Type: ACPPromptEventToolCall, ToolID: "tool-1", ToolName: "Read file", ToolTitle: "Read file", ToolArgs: `{"path":"README.md"}`},
			{Type: ACPPromptEventToolResult, ToolID: "tool-1", ToolName: "Read file", Result: "README contents"},
			{Type: ACPPromptEventText, Text: "Final markdown"},
		},
	}
	runMgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 1})
	defer runMgr.Shutdown()
	tool := DelegateTool{Manager: mgr, SubAgentManager: runMgr}

	input, _ := json.Marshal(map[string]string{
		"agent":       "copilot",
		"prompt":      "render markdown",
		"description": "Checking markdown output",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Started delegated agent") {
		t.Fatalf("expected async start message, got %q", result.Content)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	agents := runMgr.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent run, got %d", len(agents))
	}
	snap, err := subagent.WaitForSnapshot(ctx, runMgr, agents[0].ID, 2*time.Second)
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if snap.Status != subagent.StatusCompleted {
		t.Fatalf("expected completed status, got %s", snap.Status)
	}
	if !strings.Contains(snap.Result, "## Heading") {
		t.Fatalf("expected delegate output in result, got %q", snap.Result)
	}
	if len(snap.Events) < 3 {
		t.Fatalf("expected streamed events to be recorded, got %d", len(snap.Events))
	}
	if snap.Events[1].ToolID != "tool-1" {
		t.Fatalf("expected tool call id to be preserved, got %+v", snap.Events[1])
	}
}

func TestDelegateToolExecuteAsyncUsesSelfContainedToolResultMetadata(t *testing.T) {
	mgr := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"copilot": {"GitHub Copilot", "AI assistant"},
		},
		promptResult: &ACPPromptResult{Text: "done"},
		promptEvents: []ACPPromptEvent{
			{
				Type:      ACPPromptEventToolResult,
				ToolID:    "tool-2",
				ToolName:  "write_file",
				ToolTitle: "Write /tmp/hello.txt",
				ToolArgs:  `{"path":"/tmp/hello.txt","content":"hello"}`,
				Result:    "completed",
			},
		},
	}
	runMgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 1})
	defer runMgr.Shutdown()
	tool := DelegateTool{Manager: mgr, SubAgentManager: runMgr}

	input, _ := json.Marshal(map[string]string{
		"agent":       "copilot",
		"prompt":      "write file",
		"description": "Checking tool result metadata",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	agents := runMgr.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent run, got %d", len(agents))
	}
	snap, err := subagent.WaitForSnapshot(ctx, runMgr, agents[0].ID, 2*time.Second)
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if len(snap.Events) == 0 {
		t.Fatalf("expected tool result event to be recorded")
	}
	event := snap.Events[0]
	if event.ToolDisplayName != "Write" {
		t.Fatalf("expected self-contained tool title to be used, got %+v", event)
	}
	if event.ToolDetail != "/tmp/hello.txt" {
		t.Fatalf("expected self-contained tool detail to be used, got %+v", event)
	}
	if event.ToolArgs != `{"path":"/tmp/hello.txt","content":"hello"}` {
		t.Fatalf("expected self-contained tool args to be used, got %+v", event)
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
