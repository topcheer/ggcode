package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func TestListAgents_Empty(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	la := ListAgentsTool{Manager: mgr}
	result, err := la.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "No sub-agents") {
		t.Errorf("expected empty message, got: %s", result.Content)
	}
}

func TestSendMessage_MissingTo(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	sm := SendMessageTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"message": "hello",
	})
	result, err := sm.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing to")
	}
}

func TestSendMessage_MissingMessage(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	sm := SendMessageTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"to": "agent-1",
	})
	result, err := sm.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing message")
	}
}

func TestSendMessage_BroadcastEmpty(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	sm := SendMessageTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"to":      "*",
		"message": "hello all",
	})
	result, err := sm.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAny(result.Content, "No running agents") {
		t.Errorf("expected no agents message, got: %s", result.Content)
	}
}

func TestSendMessage_TargetNotFound(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	sm := SendMessageTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"to":      "agent-999",
		"message": "hello",
	})
	result, err := sm.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent agent")
	}
}

func TestWaitAgent_MissingID(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	wa := WaitAgentTool{Manager: mgr}
	result, err := wa.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing agent_id")
	}
}

func TestWaitAgent_NotFound(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	wa := WaitAgentTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"agent_id": "agent-999",
	})
	result, err := wa.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent agent")
	}
}

func TestWaitAgentToolReturnsRunningSnapshot(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("watch background test job", "watch background test job", nil, context.Background())
	mgr.SetCancel(id, func() {})
	sa, _ := mgr.Get(id)
	sa.CurrentPhase = "tool"
	sa.CurrentTool = "wait_command"
	sa.ProgressSummary = "Job ID: cmd-1 • Status: running • Total lines: 42"
	sa.ToolCallCount = 1

	tool := WaitAgentTool{Manager: mgr}
	input, _ := json.Marshal(map[string]any{
		"agent_id":     id,
		"wait_seconds": 1,
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("expected non-error result, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "[running]") || !strings.Contains(result.Content, "Progress: Job ID: cmd-1") {
		t.Fatalf("unexpected wait snapshot: %q", result.Content)
	}
}

func TestListAgentsToolShowsProgressSummary(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("watch background test job", "watch background test job", nil, context.Background())
	mgr.SetCancel(id, func() {})
	sa, _ := mgr.Get(id)
	sa.ProgressSummary = "Job ID: cmd-1 • Status: running • Total lines: 42"

	tool := ListAgentsTool{Manager: mgr}
	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("expected non-error result, got %q", result.Content)
	}
	if !strings.Contains(result.Content, id) || !strings.Contains(result.Content, "Progress: Job ID: cmd-1") {
		t.Fatalf("unexpected list output: %q", result.Content)
	}
}
