package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func TestIssue4139_CompletedRunWithResidualProgressReturnsFullResult(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 2})
	defer mgr.Shutdown()
	id := mgr.Spawn("tester", "run tests and report", "", nil, context.Background())

	mgr.UpdateProgress(id, "wait_command finished")
	long := strings.Repeat("E", 260) + "-REPORT-TAIL"
	mgr.Complete(id, long, nil)

	wa := WaitAgentTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"agent_id":     id,
		"wait_seconds": 1,
		"description":  "waiting for tester",
	})
	res, err := wa.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("wait_agent execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "-REPORT-TAIL") {
		t.Fatalf("full result must survive past the 200-char truncation point (#4138-style silent cut); got %d chars: %.300s", len(res.Content), res.Content)
	}
}

func TestIssue4139_CleanCompletedRunKeepsFullResult(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 2})
	defer mgr.Shutdown()
	id := mgr.Spawn("clean", "task", "", nil, context.Background())
	long := strings.Repeat("F", 260) + "-CLEAN-TAIL"
	mgr.Complete(id, long, nil)

	wa := WaitAgentTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"agent_id":     id,
		"wait_seconds": 1,
		"description":  "waiting for clean",
	})
	res, err := wa.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("wait_agent execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "-CLEAN-TAIL") {
		t.Fatalf("clean-completion fast path must keep returning the full result; got %d chars", len(res.Content))
	}
}

func TestIssue4139_RunningRunStillReturnsSnapshot(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 2})
	defer mgr.Shutdown()
	id := mgr.Spawn("busy", "task", "", nil, context.Background())
	mgr.UpdateProgress(id, "still working")

	wa := WaitAgentTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"agent_id":     id,
		"wait_seconds": 1,
		"description":  "waiting for busy",
	})
	res, err := wa.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("wait_agent execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "still working") {
		t.Fatalf("running run must keep the progress-snapshot output; got: %.200s", res.Content)
	}
}
