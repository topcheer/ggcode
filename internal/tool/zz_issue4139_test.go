package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

// #4139: a completed run whose last tool was a command tool carries a
// residual ProgressSummary (Complete clears CurrentTool but nothing clears
// ProgressSummary). Such runs must still return their FULL result from
// wait_agent - pre-fix they fell to the slow path, where the
// list_agents-styled formatter truncated Result to 200 chars, silently
// dropping the delivery report the acceptance-criteria reminders rely on.

func TestIssue4139_CompletedRunWithResidualProgressReturnsFullResult(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 2})
	defer mgr.Shutdown()
	id := mgr.Spawn("tester", "run tests and report", "", nil, context.Background())

	// Simulate a command-tool finish: progress summary set, then terminal
	// Complete with a >200-char delivery report.
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

// Regression: the clean-completion fast path (no residual summary) keeps
// returning the full result.
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

// Regression: a still-running run keeps the status-snapshot output (the
// formatter path stays for non-terminal states).
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
