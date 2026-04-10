package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

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
