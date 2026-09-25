package agent

// zz_r73_consensus_escalation_test.go - loop-level tests for the consensus
// escalation failsafe (r73, arXiv:2606.27009 semantic halting).
//
// The state machine is unit-tested in consensus_test.go (tier boundary,
// latch, nil safety). These tests verify the WIRING inside the real agent
// loop driven through mockProvider:
//  1. once the escalation threshold is armed DURING tool execution (as the
//     live detector chain would do via repeated consensus alerts), the loop
//     hard-stops after that round's tool results are committed (sentinel
//     error, no further LLM calls, tool_use/tool_result pairs balanced);
//  2. below the threshold (single alert) the loop runs to normal completion.
//
// Note: RunStreamWithContent resets crossDetectorConsensus at run start
// (per-turn counter), so the state cannot be pre-seeded before RunStream -
// the arming tool below mutates it mid-run instead, mirroring where the real
// consensus checkOnly() call site sits (tool-result processing, loop tail).

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// armingTool sets the agent's consensus alerts counter to a fixed value the
// first time it executes, then returns a normal result.
type armingTool struct {
	name   string
	agent  *Agent
	alerts int
	once   sync.Once
}

func (t *armingTool) Name() string                { return t.name }
func (t *armingTool) Description() string         { return "arms consensus escalation state" }
func (t *armingTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *armingTool) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	t.once.Do(func() {
		t.agent.crossDetectorConsensus.alertsIssued = t.alerts
		t.agent.crossDetectorConsensus.lastAlertStep = -consensusCooldownSteps
	})
	return tool.Result{Content: "armed r73 state"}, nil
}

func r73EscalationTestAgent(t *testing.T, alerts int) (*Agent, *mockProvider) {
	t.Helper()
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "tc1", Name: "grep", Arguments: json.RawMessage(`{}`)}}},
			{{Type: provider.StreamEventText, Text: "second round should never happen"}},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "sys", 10)
	t.Cleanup(func() { a.Close() })
	if err := a.tools.Register(&armingTool{name: "grep", agent: a, alerts: alerts}); err != nil {
		t.Fatalf("register arming tool: %v", err)
	}
	return a, mp
}

func TestR73ConsensusEscalation_HardStopsLoop(t *testing.T) {
	a, mp := r73EscalationTestAgent(t, consensusMaxAlerts)

	var texts []string
	err := a.RunStream(t.Context(), "start", func(ev provider.StreamEvent) {
		if ev.Type == provider.StreamEventText {
			texts = append(texts, ev.Text)
		}
	})
	if err == nil {
		t.Fatal("expected sentinel escalation error, got nil")
	}
	if !strings.Contains(err.Error(), "cross-detector consensus escalation") {
		t.Fatalf("unexpected sentinel error: %v", err)
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "Repeated systemic failure detected") {
		t.Fatalf("expected user-facing escalation summary event, got %q", joined)
	}

	// No further LLM call after the halt: only round 1 hit the provider.
	if mp.streamCalls != 1 {
		t.Fatalf("streamCalls = %d, want 1 (loop must not continue past escalation)", mp.streamCalls)
	}

	// Tool-use/tool-result pairing: the assistant tool_use must be followed
	// by a user message carrying the tool result, so the next turn's context
	// stays protocol-valid.
	msgs := a.Messages()
	sawToolUse, sawResult := false, false
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, b := range m.Content {
				if b.Type == "tool_use" {
					sawToolUse = true
				}
			}
		}
		if m.Role == "user" {
			for _, b := range m.Content {
				if b.Type == "tool_result" {
					sawResult = true
				}
			}
		}
	}
	if !sawToolUse || !sawResult {
		t.Fatalf("tool_use=%v tool_result=%v after escalation halt (pairs must stay balanced)", sawToolUse, sawResult)
	}
}

func TestR73ConsensusEscalation_NoHaltBelowThreshold(t *testing.T) {
	a, mp := r73EscalationTestAgent(t, consensusMaxAlerts-1)

	err := a.RunStream(t.Context(), "start", func(provider.StreamEvent) {})
	if err != nil {
		t.Fatalf("single consensus alert must NOT halt the loop, got: %v", err)
	}
	// Both rounds ran: tool call, then the normal final text.
	if mp.streamCalls != 2 {
		t.Fatalf("streamCalls = %d, want 2 (loop should complete normally)", mp.streamCalls)
	}
}
