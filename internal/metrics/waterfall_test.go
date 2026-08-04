package metrics

import (
	"testing"
	"time"
)

func TestAnalyzeWaterfall_Empty(t *testing.T) {
	result := AnalyzeWaterfall(nil)
	if result != nil {
		t.Errorf("expected nil for empty events, got %v", result)
	}
}

func TestAnalyzeWaterfall_SequentialTools(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Two sequential tools: tool1 [0s-2s], tool2 [2s-4s]
	events := []MetricEvent{
		{TurnIndex: 1, Type: "tool", ToolName: "tool1", Timestamp: base.Add(2 * time.Second),
			ToolDuration: 2 * time.Second, ToolSuccess: true},
		{TurnIndex: 1, Type: "tool", ToolName: "tool2", Timestamp: base.Add(4 * time.Second),
			ToolDuration: 2 * time.Second, ToolSuccess: true},
	}

	result := AnalyzeWaterfall(events)
	if len(result) != 1 {
		t.Fatalf("expected 1 turn analysis, got %d", len(result))
	}
	w := result[0]
	if w.TurnIndex != 1 {
		t.Errorf("expected turn 1, got %d", w.TurnIndex)
	}
	if w.WallClock != 4*time.Second {
		t.Errorf("expected wall clock 4s, got %v", w.WallClock)
	}
	if w.TotalToolTime != 4*time.Second {
		t.Errorf("expected total tool time 4s, got %v", w.TotalToolTime)
	}
	// Fully sequential: overlap ratio should be 0.
	if w.OverlapRatio != 0 {
		t.Errorf("expected overlap ratio 0 for sequential tools, got %v", w.OverlapRatio)
	}
	// No parallel groups since tools don't overlap.
	if len(w.ParallelToolGroups) != 0 {
		t.Errorf("expected 0 parallel groups, got %d", len(w.ParallelToolGroups))
	}
	// Critical path should be 4s (both tools sequential).
	if w.CriticalPathDuration != 4*time.Second {
		t.Errorf("expected critical path 4s, got %v", w.CriticalPathDuration)
	}
}

func TestAnalyzeWaterfall_ParallelTools(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Two parallel tools: tool1 [0s-3s], tool2 [1s-2s] (overlapping)
	events := []MetricEvent{
		{TurnIndex: 1, Type: "tool", ToolName: "tool1", Timestamp: base.Add(3 * time.Second),
			ToolDuration: 3 * time.Second, ToolSuccess: true},
		{TurnIndex: 1, Type: "tool", ToolName: "tool2", Timestamp: base.Add(2 * time.Second),
			ToolDuration: 1 * time.Second, ToolSuccess: true},
	}

	result := AnalyzeWaterfall(events)
	w := result[0]
	// Wall clock = 3s (earliest start to latest end).
	if w.WallClock != 3*time.Second {
		t.Errorf("expected wall clock 3s, got %v", w.WallClock)
	}
	// Total tool time = 3s + 1s = 4s.
	if w.TotalToolTime != 4*time.Second {
		t.Errorf("expected total tool time 4s, got %v", w.TotalToolTime)
	}
	// Should detect 1 parallel group with 2 tools.
	if len(w.ParallelToolGroups) != 1 {
		t.Fatalf("expected 1 parallel group, got %d", len(w.ParallelToolGroups))
	}
	if len(w.ParallelToolGroups[0]) != 2 {
		t.Errorf("expected 2 tools in parallel group, got %d", len(w.ParallelToolGroups[0]))
	}
	// Overlap ratio > 0.
	if w.OverlapRatio <= 0 {
		t.Errorf("expected positive overlap ratio, got %v", w.OverlapRatio)
	}
}

func TestAnalyzeWaterfall_Bottleneck(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	events := []MetricEvent{
		{TurnIndex: 1, Type: "tool", ToolName: "fast", Timestamp: base.Add(100 * time.Millisecond),
			ToolDuration: 100 * time.Millisecond, ToolSuccess: true},
		{TurnIndex: 1, Type: "tool", ToolName: "slow", Timestamp: base.Add(5 * time.Second),
			ToolDuration: 5 * time.Second, ToolSuccess: true},
	}
	result := AnalyzeWaterfall(events)
	w := result[0]
	if w.BottleneckTool != "slow" {
		t.Errorf("expected bottleneck 'slow', got %q", w.BottleneckTool)
	}
	if w.BottleneckDuration != 5*time.Second {
		t.Errorf("expected bottleneck duration 5s, got %v", w.BottleneckDuration)
	}
}

func TestAnalyzeWaterfall_LLMTime(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	events := []MetricEvent{
		{TurnIndex: 1, Type: "llm", Timestamp: base.Add(3 * time.Second), Duration: 3 * time.Second},
		{TurnIndex: 1, Type: "tool", ToolName: "bash", Timestamp: base.Add(4 * time.Second),
			ToolDuration: 1 * time.Second, ToolSuccess: true},
	}
	result := AnalyzeWaterfall(events)
	w := result[0]
	if w.LLMTime != 3*time.Second {
		t.Errorf("expected LLM time 3s, got %v", w.LLMTime)
	}
	if w.TotalToolTime != 1*time.Second {
		t.Errorf("expected total tool time 1s, got %v", w.TotalToolTime)
	}
}

func TestAnalyzeWaterfall_MultipleTurns(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	events := []MetricEvent{
		{TurnIndex: 1, Type: "llm", Timestamp: base.Add(2 * time.Second), Duration: 2 * time.Second},
		{TurnIndex: 2, Type: "llm", Timestamp: base.Add(5 * time.Second), Duration: 3 * time.Second},
		{TurnIndex: 2, Type: "tool", ToolName: "grep", Timestamp: base.Add(6 * time.Second),
			ToolDuration: 1 * time.Second, ToolSuccess: true},
	}
	result := AnalyzeWaterfall(events)
	if len(result) != 2 {
		t.Fatalf("expected 2 turn analyses, got %d", len(result))
	}
	if result[0].TurnIndex != 1 {
		t.Errorf("expected first turn 1, got %d", result[0].TurnIndex)
	}
	if result[1].TurnIndex != 2 {
		t.Errorf("expected second turn 2, got %d", result[1].TurnIndex)
	}
}

func TestAnalyzeWaterfall_CriticalPathLongChain(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Chain of 3 sequential tools, plus a parallel short tool
	events := []MetricEvent{
		{TurnIndex: 1, Type: "tool", ToolName: "a", Timestamp: base.Add(2 * time.Second),
			ToolDuration: 2 * time.Second, ToolSuccess: true}, // [0s-2s]
		{TurnIndex: 1, Type: "tool", ToolName: "b", Timestamp: base.Add(4 * time.Second),
			ToolDuration: 2 * time.Second, ToolSuccess: true}, // [2s-4s]
		{TurnIndex: 1, Type: "tool", ToolName: "c", Timestamp: base.Add(7 * time.Second),
			ToolDuration: 3 * time.Second, ToolSuccess: true}, // [4s-7s]
	}
	result := AnalyzeWaterfall(events)
	w := result[0]
	// Critical path should be a+b+c = 7s
	if w.CriticalPathDuration != 7*time.Second {
		t.Errorf("expected critical path 7s, got %v", w.CriticalPathDuration)
	}
	if len(w.SequentialChain) != 3 {
		t.Errorf("expected 3 items in sequential chain, got %d", len(w.SequentialChain))
	}
}

func TestFormatWaterfallSummary(t *testing.T) {
	turns := []WaterfallAnalysis{
		{TurnIndex: 1, WallClock: 5 * time.Second, TotalToolTime: 3 * time.Second,
			LLMTime: 2 * time.Second, OverlapRatio: 0.5, CriticalPathDuration: 3 * time.Second,
			BottleneckTool: "build", BottleneckDuration: 2 * time.Second},
	}
	s := FormatWaterfallSummary(turns)
	if s == "" {
		t.Fatal("expected non-empty summary")
	}
	if !contains(s, "Turn 1") {
		t.Errorf("expected 'Turn 1' in summary, got: %s", s)
	}
	if !contains(s, "bottleneck=build") {
		t.Errorf("expected bottleneck=build in summary, got: %s", s)
	}
}

func TestFormatWaterfallSummary_Empty(t *testing.T) {
	s := FormatWaterfallSummary(nil)
	if s != "" {
		t.Errorf("expected empty string for nil input, got %q", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
