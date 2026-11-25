package agent

// #5013 call-semantics companions (spawn_agent max_tool_calls): the
// subagent runner wires cfg.MaxToolCalls through SetToolCallBudget so the
// parameter bounds ACTUAL tool invocations at the exec layer, not just LLM
// turns. These tests pin the agent-side half of that contract:
//   - a parallel batch larger than the budget hard-stops mid-batch with the
//     budget-exhausted error (calls beyond the budget never execute)
//   - an explicit SetToolCallBudget is the only thing that arms the gate
//   - no budget set (0) never stops on call count alone (no derived default)

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// issue5013CountingTool counts its executions; each call's result content
// carries its sequence number so the test can prove which calls ran.
type issue5013CountingTool struct {
	name  string
	count *int32
}

func (t issue5013CountingTool) Name() string        { return t.name }
func (t issue5013CountingTool) Description() string { return "counting tool" }
func (t issue5013CountingTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t issue5013CountingTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	n := atomic.AddInt32(t.count, 1)
	return tool.Result{Content: fmt.Sprintf("call #%d", n)}, nil
}

// issue5013BatchEvents builds one LLM turn that issues n tool calls in a
// single parallel batch (the sa-18 shape: many calls, one turn).
func issue5013BatchEvents(prefix string, n int) []provider.StreamEvent {
	events := make([]provider.StreamEvent, 0, n+1)
	for i := 0; i < n; i++ {
		events = append(events, provider.StreamEvent{
			Type: provider.StreamEventToolCallDone,
			Tool: provider.ToolCallDelta{ID: fmt.Sprintf("%s-%d", prefix, i), Name: "counter", Arguments: json.RawMessage(`{}`)},
		})
	}
	events = append(events, provider.StreamEvent{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 5, OutputTokens: 1}})
	return events
}

func TestIssue5013CallBudgetStopsOversizedBatchMidTurn(t *testing.T) {
	// Budget 3, one turn batching 5 calls: the exec-layer gate must stop
	// the run after the budget is crossed - exactly the incident shape
	// (248 calls under a 12-call task budget) the turn gate could not
	// catch because the run never exceeded one turn per few calls.
	var executed int32
	registry := tool.NewRegistry()
	if err := registry.Register(issue5013CountingTool{name: "counter", count: &executed}); err != nil {
		t.Fatal(err)
	}

	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			issue5013BatchEvents("tc", 5),
			// Second turn would run if the budget gate failed to stop.
			issue5013BatchEvents("tc2", 5),
		},
	}
	a := NewAgent(mp, registry, "", 10)
	a.SetToolCallBudget(3)

	err := a.RunStream(context.Background(), "loop", func(provider.StreamEvent) {})
	if err == nil {
		t.Fatal("run must stop with the budget-exhausted error")
	}
	if !strings.Contains(err.Error(), "tool call budget") || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("run error = %v, want tool call budget exhausted", err)
	}
	if got := atomic.LoadInt32(&executed); got > 5 {
		t.Fatalf("executed = %d, want <= 5 (stop gates the batch, not just next turn)", got)
	}
	if got := atomic.LoadInt32(&executed); got < 3 {
		t.Fatalf("executed = %d, want >= 3 (budgeted calls must run)", got)
	}
	if mp.streamCalls > 2 {
		t.Fatalf("provider turns = %d, want <= 2 (run must not keep looping past the gate)", mp.streamCalls)
	}
}

func TestIssue5013ExplicitBudgetIsTheOnlyBudget(t *testing.T) {
	// There is no derived default anymore: the explicit budget of 4 is the
	// only cap, and a 6-call batch must trip it even with maxIter=60.
	var executed int32
	registry := tool.NewRegistry()
	if err := registry.Register(issue5013CountingTool{name: "counter", count: &executed}); err != nil {
		t.Fatal(err)
	}

	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			issue5013BatchEvents("tc", 6),
		},
	}
	a := NewAgent(mp, registry, "", 60)
	a.SetToolCallBudget(4)

	err := a.RunStream(context.Background(), "loop", func(provider.StreamEvent) {})
	if err == nil || !strings.Contains(err.Error(), "tool call budget") {
		t.Fatalf("run error = %v, want tool call budget exhaustion (explicit 4)", err)
	}
}

func TestIssue5013NoBudgetSetNeverStopsOnCallCount(t *testing.T) {
	// Without SetToolCallBudget there is NO budget (no derived default): the
	// gate never fires on call count alone, however long the run is.
	var executed int32
	registry := tool.NewRegistry()
	if err := registry.Register(issue5013CountingTool{name: "counter", count: &executed}); err != nil {
		t.Fatal(err)
	}

	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "tc-0", Name: "counter", Arguments: json.RawMessage(`{}`)}},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 5, OutputTokens: 1}},
			},
			{
				{Type: provider.StreamEventText, Text: "done"},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 5, OutputTokens: 1}},
			},
		},
	}
	a := NewAgent(mp, registry, "", 10)

	if err := a.RunStream(context.Background(), "work", func(provider.StreamEvent) {}); err != nil {
		t.Fatalf("run error = %v, want clean completion (no explicit budget -> no gate)", err)
	}
	if got := atomic.LoadInt32(&executed); got != 1 {
		t.Fatalf("executed = %d, want 1", got)
	}
}

// TestNoBudgetLongRunNeverStops pins the removal of the derived default and
// of the forced caps: an unlimited run (maxIter 0, no tool_call_budget) makes
// more calls than the former 500-call default and more turns than the former
// 100/60 caps, and must still complete cleanly on the model's own text turn.
func TestNoBudgetLongRunNeverStops(t *testing.T) {
	var executed int32
	registry := tool.NewRegistry()
	if err := registry.Register(issue5013CountingTool{name: "counter", count: &executed}); err != nil {
		t.Fatal(err)
	}
	turns := make([][]provider.StreamEvent, 0, 111)
	for i := 0; i < 110; i++ {
		turns = append(turns, issue5013BatchEvents(fmt.Sprintf("tc%d", i), 5)) // 550 calls
	}
	turns = append(turns, []provider.StreamEvent{
		{Type: provider.StreamEventText, Text: "done"},
		{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 5, OutputTokens: 1}},
	})
	mp := &mockProvider{streamEvents: turns}
	a := NewAgent(mp, registry, "", 0) // unlimited iterations, no budget

	if err := a.RunStream(context.Background(), "work", func(provider.StreamEvent) {}); err != nil {
		t.Fatalf("run error = %v, want clean completion across 110 turns / 550 calls", err)
	}
	if got := atomic.LoadInt32(&executed); got != 550 {
		t.Fatalf("executed = %d, want 550", got)
	}
}
