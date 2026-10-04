package metrics

import (
	"testing"
	"time"
)

// #3295 regression: same-turn sub-agent events (AgentID != "") must not
// pollute the turn's latency stats - a faster sub-agent first packet used
// to win the TTFT min, and cross-model decode speeds mixed into one TPS
// average. Delegated activity is counted separately instead.
func TestSummarizeDelegatedIsolation(t *testing.T) {
	main := MetricEvent{
		Type: "llm", TurnIndex: 3,
		TTFT: 800 * time.Millisecond, Duration: 5 * time.Second,
		InputTokens: 1000, OutputTokens: 500,
	}
	sub := MetricEvent{
		Type: "llm", TurnIndex: 3, AgentID: "sub-1",
		TTFT: 50 * time.Millisecond, Duration: 1 * time.Second,
		InputTokens: 20000, OutputTokens: 9000,
	}
	subTool := MetricEvent{Type: "tool", TurnIndex: 3, AgentID: "sub-1", ToolName: "grep"}

	s := Summarize([]MetricEvent{main, sub, subTool})
	if len(s.Turns) == 0 {
		t.Fatal("no turns summarized")
	}
	turn := s.Turns[len(s.Turns)-1]
	if turn.TurnIndex != 3 {
		t.Fatalf("turn index = %d, want 3", turn.TurnIndex)
	}
	if turn.TTFT != 800*time.Millisecond {
		t.Fatalf("TTFT = %v, want the main agent's 800ms (sub-agent 50ms must not win the min)", turn.TTFT)
	}
	if turn.InputTokens != 1000 || turn.OutputTokens != 500 {
		t.Fatalf("main tokens polluted: in=%d out=%d, want 1000/500", turn.InputTokens, turn.OutputTokens)
	}
	if turn.LLMCallCount != 1 {
		t.Fatalf("LLMCallCount = %d, want 1 (sub-agent call counted as delegated)", turn.LLMCallCount)
	}
	if turn.ToolCallCount != 0 {
		t.Fatalf("ToolCallCount = %d, want 0 (sub-agent tool counted as delegated)", turn.ToolCallCount)
	}
	if turn.DelegatedLLMCalls != 1 || turn.DelegatedToolCalls != 1 {
		t.Fatalf("delegated counts = %d/%d, want 1/1", turn.DelegatedLLMCalls, turn.DelegatedToolCalls)
	}
	if turn.DelegatedInputTokens != 20000 || turn.DelegatedOutputTokens != 9000 {
		t.Fatalf("delegated tokens = %d/%d, want 20000/9000", turn.DelegatedInputTokens, turn.DelegatedOutputTokens)
	}
}

// Parent events (AgentID == "") keep the exact pre-#3295 aggregation shape.
func TestSummarizeParentEventsUnchanged(t *testing.T) {
	evs := []MetricEvent{
		{Type: "llm", TurnIndex: 1, TTFT: 300 * time.Millisecond, Duration: 2 * time.Second, InputTokens: 10, OutputTokens: 20},
		{Type: "tool", TurnIndex: 1, ToolName: "read_file", ToolSuccess: true},
	}
	s := Summarize(evs)
	turn := s.Turns[len(s.Turns)-1]
	if turn.LLMCallCount != 1 || turn.ToolCallCount != 1 || turn.TTFT != 300*time.Millisecond {
		t.Fatalf("parent aggregation regressed: %+v", turn)
	}
	if turn.DelegatedLLMCalls != 0 || turn.DelegatedToolCalls != 0 {
		t.Fatalf("no delegation expected: %+v", turn)
	}
}
