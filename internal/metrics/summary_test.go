package metrics

import (
	"testing"
	"time"
)

func TestSummarizeAggregatesTurnsAndTools(t *testing.T) {
	summary := Summarize([]MetricEvent{
		{TurnIndex: 1, Type: "llm", TTFT: 800 * time.Millisecond, ThinkTime: 1500 * time.Millisecond, Duration: 5 * time.Second},
		{TurnIndex: 1, Type: "tool", ToolName: "bash", ToolSuccess: true, ToolDuration: 2200 * time.Millisecond},
		{TurnIndex: 1, Type: "tool", ToolName: "read_bash", ToolSuccess: false, ToolError: "timeout", ToolDuration: 3 * time.Second},
		{TurnIndex: 2, Type: "llm", TTFT: 1200 * time.Millisecond, ThinkTime: 2 * time.Second, Duration: 7 * time.Second},
		{TurnIndex: 2, Type: "llm", TTFT: 900 * time.Millisecond, ThinkTime: time.Second, Duration: 1500 * time.Millisecond},
		{TurnIndex: 2, Type: "tool", ToolName: "bash", ToolSuccess: true, ToolDuration: 1800 * time.Millisecond},
	})

	if !summary.HasData() {
		t.Fatal("expected summary to have data")
	}
	if summary.TurnCount != 2 {
		t.Fatalf("expected 2 turns, got %d", summary.TurnCount)
	}
	if summary.LLMCallCount != 3 {
		t.Fatalf("expected 3 llm calls, got %d", summary.LLMCallCount)
	}
	if summary.ToolCallCount != 3 {
		t.Fatalf("expected 3 tool calls, got %d", summary.ToolCallCount)
	}
	if summary.ToolFailureCount != 1 {
		t.Fatalf("expected 1 tool failure, got %d", summary.ToolFailureCount)
	}
	if summary.ToolFailureRate() != 33 {
		t.Fatalf("expected failure rate 33, got %d", summary.ToolFailureRate())
	}
	if summary.AvgTTFT != 850*time.Millisecond {
		t.Fatalf("expected avg ttft 850ms, got %s", summary.AvgTTFT)
	}
	if summary.P95TTFT != 900*time.Millisecond {
		t.Fatalf("expected p95 ttft 900ms, got %s", summary.P95TTFT)
	}
	if summary.AvgDuration != 6750*time.Millisecond {
		t.Fatalf("expected avg duration 6.75s, got %s", summary.AvgDuration)
	}
	if summary.P95Duration != 8500*time.Millisecond {
		t.Fatalf("expected p95 duration 8.5s, got %s", summary.P95Duration)
	}
	if summary.AvgThink != 2250*time.Millisecond {
		t.Fatalf("expected avg think 2.25s, got %s", summary.AvgThink)
	}
	if len(summary.Turns) != 2 {
		t.Fatalf("expected 2 turn summaries, got %d", len(summary.Turns))
	}
	if summary.Turns[0].SlowestTool != "read_bash" || summary.Turns[0].ToolFailureCount != 1 {
		t.Fatalf("unexpected turn 1 summary: %+v", summary.Turns[0])
	}
	if summary.Turns[1].TTFT != 900*time.Millisecond || summary.Turns[1].Duration != 8500*time.Millisecond {
		t.Fatalf("unexpected turn 2 summary: %+v", summary.Turns[1])
	}
	if len(summary.SlowTools) != 2 {
		t.Fatalf("expected 2 slow tools, got %d", len(summary.SlowTools))
	}
	if summary.SlowTools[0].Name != "read_bash" || summary.SlowTools[0].AvgDuration != 3*time.Second {
		t.Fatalf("unexpected slow tool summary: %+v", summary.SlowTools[0])
	}
	if summary.SlowTools[1].Name != "bash" || summary.SlowTools[1].AvgDuration != 2*time.Second {
		t.Fatalf("unexpected second slow tool summary: %+v", summary.SlowTools[1])
	}
}

func TestSummarizeEmpty(t *testing.T) {
	summary := Summarize(nil)
	if summary.HasData() {
		t.Fatal("expected empty summary to report no data")
	}
	if summary.TurnCount != 0 || summary.ToolFailureRate() != 0 {
		t.Fatalf("unexpected empty summary: %+v", summary)
	}
}

func TestTurnSummaryForIndex(t *testing.T) {
	events := []MetricEvent{
		{TurnIndex: 1, Type: "llm", TTFT: 800 * time.Millisecond, ThinkTime: time.Second, Duration: 5 * time.Second},
		{TurnIndex: 2, Type: "llm", TTFT: 1200 * time.Millisecond, ThinkTime: 2 * time.Second, Duration: 7 * time.Second},
		{TurnIndex: 2, Type: "tool", ToolName: "bash", ToolSuccess: true, ToolDuration: 3 * time.Second},
	}

	turn, ok := TurnSummaryForIndex(events, 2)
	if !ok {
		t.Fatal("expected turn 2 summary")
	}
	if turn.TurnIndex != 2 || turn.ToolCallCount != 1 || turn.SlowestTool != "bash" {
		t.Fatalf("unexpected turn summary: %+v", turn)
	}

	if _, ok := TurnSummaryForIndex(events, 3); ok {
		t.Fatal("expected missing turn summary for turn 3")
	}
}

func TestSummarizeTokenAggregation(t *testing.T) {
	events := []MetricEvent{
		{TurnIndex: 1, Type: "llm", InputTokens: 100, OutputTokens: 50, CacheRead: 200},
		{TurnIndex: 2, Type: "llm", InputTokens: 300, OutputTokens: 70, CacheRead: 50},
	}
	summary := Summarize(events)
	if summary.TotalInputTokens != 400 {
		t.Errorf("TotalInputTokens = %d, want 400", summary.TotalInputTokens)
	}
	if summary.TotalOutputTokens != 120 {
		t.Errorf("TotalOutputTokens = %d, want 120", summary.TotalOutputTokens)
	}
	if len(summary.Turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(summary.Turns))
	}
	if summary.Turns[0].CumInputTokens != 100 {
		t.Errorf("turn 1 CumInputTokens = %d, want 100", summary.Turns[0].CumInputTokens)
	}
	if summary.Turns[1].CumInputTokens != 400 {
		t.Errorf("turn 2 CumInputTokens = %d, want 400", summary.Turns[1].CumInputTokens)
	}
	if summary.Turns[1].CumOutputTokens != 120 {
		t.Errorf("turn 2 CumOutputTokens = %d, want 120", summary.Turns[1].CumOutputTokens)
	}
}
