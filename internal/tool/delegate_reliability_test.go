package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agentreliability"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func newReliabilityForTest(t *testing.T) *agentreliability.Reliability {
	t.Helper()
	return agentreliability.New(filepath.Join(t.TempDir(), "agent-reliability.json"))
}

func TestDelegateRecordsReliabilityOnSuccess(t *testing.T) {
	mgr := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"codex": {"Codex", "Coding agent"},
		},
	}
	rel := newReliabilityForTest(t)
	tool := DelegateTool{Manager: mgr, Reliability: rel}

	input, _ := json.Marshal(map[string]string{"agent": "codex", "prompt": "do work"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Reliability (recent): codex: 1/1 completed recently") {
		t.Fatalf("expected reliability hint in result, got: %s", result.Content)
	}
}

func TestDelegateRecordsReliabilityOnPromptError(t *testing.T) {
	mgr := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"codex": {"Codex", "Coding agent"},
		},
		promptErr: context.DeadlineExceeded,
	}
	rel := newReliabilityForTest(t)
	tool := DelegateTool{Manager: mgr, Reliability: rel}

	input, _ := json.Marshal(map[string]string{"agent": "codex", "prompt": "do work"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
	if !strings.Contains(result.Content, "Reliability (recent): codex: 0/1 completed recently") {
		t.Fatalf("expected reliability hint on error path, got: %s", result.Content)
	}
}

func TestDelegateRecordsReliabilityOnUnavailableAgent(t *testing.T) {
	mgr := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"codex": {"Codex", "Coding agent"},
		},
	}
	rel := newReliabilityForTest(t)
	tool := DelegateTool{Manager: mgr, Reliability: rel}

	input, _ := json.Marshal(map[string]string{"agent": "nonexistent", "prompt": "do work"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for nonexistent agent")
	}
	// Launch failures are reliability-relevant and must be recorded.
	if got := rel.AgentSummary("nonexistent"); !strings.Contains(got, "0/1 completed recently") {
		t.Fatalf("expected failed record for unavailable agent, got summary %q", got)
	}
}

func TestDelegateNilReliabilitySkipsHint(t *testing.T) {
	mgr := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"codex": {"Codex", "Coding agent"},
		},
	}
	tool := DelegateTool{Manager: mgr}

	input, _ := json.Marshal(map[string]string{"agent": "codex", "prompt": "do work"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if strings.Contains(result.Content, "Reliability (recent)") {
		t.Fatalf("nil reliability must not append hint, got: %s", result.Content)
	}
}

func TestListAgentsAppendsReliabilityOverview(t *testing.T) {
	rel := newReliabilityForTest(t)
	rel.Record("codex", agentreliability.OutcomeCompleted, time.Now(), 0)
	rel.Record("codex", agentreliability.OutcomeFailed, time.Now(), 0)

	tool := ListAgentsTool{
		Manager:     subagent.NewManager(config.SubAgentConfig{}),
		Reliability: rel,
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"description":"list"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "ACP delegate reliability (recent): codex 1/2 ok") {
		t.Fatalf("expected reliability overview in list_agents output, got: %s", result.Content)
	}
}

func TestListAgentsOmitsEmptyReliability(t *testing.T) {
	tool := ListAgentsTool{
		Manager:     subagent.NewManager(config.SubAgentConfig{}),
		Reliability: newReliabilityForTest(t),
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"description":"list"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Content, "ACP delegate reliability") {
		t.Fatalf("empty ledger must not append section, got: %s", result.Content)
	}
}
