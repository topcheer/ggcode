package metrics

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildSpanTree_Empty(t *testing.T) {
	tree := BuildSpanTree("empty-session", nil)
	if tree.Kind != "session" {
		t.Errorf("expected kind 'session', got %q", tree.Kind)
	}
	if tree.SpanID == "" {
		t.Error("expected non-empty span ID")
	}
	if len(tree.Children) != 0 {
		t.Errorf("expected 0 children, got %d", len(tree.Children))
	}
}

func TestBuildSpanTree_Hierarchy(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	events := []MetricEvent{
		{TurnIndex: 1, Type: "llm", Timestamp: base.Add(5 * time.Second), Duration: 5 * time.Second,
			TTFT: 800 * time.Millisecond, InputTokens: 1000, OutputTokens: 200, Model: "test-model"},
		{TurnIndex: 1, Type: "tool", ToolName: "read_file", Timestamp: base.Add(6100 * time.Millisecond),
			ToolDuration: 100 * time.Millisecond, ToolSuccess: true},
		{TurnIndex: 2, Type: "llm", Timestamp: base.Add(10 * time.Second), Duration: 3 * time.Second,
			InputTokens: 1500, OutputTokens: 300},
	}

	tree := BuildSpanTree("test-session", events)

	if tree.Kind != "session" {
		t.Errorf("expected root kind 'session', got %q", tree.Kind)
	}
	if len(tree.Children) != 2 {
		t.Fatalf("expected 2 turn children, got %d", len(tree.Children))
	}

	// First child should be turn 1.
	turn1 := tree.Children[0]
	if turn1.Kind != "turn" {
		t.Errorf("expected turn kind, got %q", turn1.Kind)
	}
	if len(turn1.Children) != 2 {
		t.Fatalf("expected 2 children in turn 1, got %d", len(turn1.Children))
	}

	// Children should be llm and tool.
	if turn1.Children[0].Kind != "llm" {
		t.Errorf("expected first child kind 'llm', got %q", turn1.Children[0].Kind)
	}
	if turn1.Children[1].Kind != "tool" {
		t.Errorf("expected second child kind 'tool', got %q", turn1.Children[1].Kind)
	}

	// LLM span should have model in attributes.
	llmSpan := turn1.Children[0]
	if llmSpan.Attributes["model"] != "test-model" {
		t.Errorf("expected model attr 'test-model', got %v", llmSpan.Attributes["model"])
	}
	if llmSpan.Attributes["input_tokens"].(int) != 1000 {
		t.Errorf("expected input_tokens 1000, got %v", llmSpan.Attributes["input_tokens"])
	}

	// Tool span should have tool_name in attributes.
	toolSpan := turn1.Children[1]
	if toolSpan.Attributes["tool_name"] != "read_file" {
		t.Errorf("expected tool_name 'read_file', got %v", toolSpan.Attributes["tool_name"])
	}
}

func TestBuildSpanTree_StartTimeReconstruction(t *testing.T) {
	end := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	events := []MetricEvent{
		{TurnIndex: 1, Type: "tool", ToolName: "slow_tool", Timestamp: end, ToolDuration: 3 * time.Second, ToolSuccess: true},
	}
	tree := BuildSpanTree("s", events)
	toolSpan := tree.Children[0].Children[0]
	expectedStart := end.Add(-3 * time.Second)
	if !toolSpan.StartTime.Equal(expectedStart) {
		t.Errorf("expected start %v, got %v", expectedStart, toolSpan.StartTime)
	}
	if toolSpan.EndTime != end {
		t.Errorf("expected end %v, got %v", end, toolSpan.EndTime)
	}
}

func TestBuildSpanTree_DeterministicSpanIDs(t *testing.T) {
	events := []MetricEvent{
		{TurnIndex: 1, Type: "llm", Timestamp: time.Now(), Duration: time.Second},
		{TurnIndex: 1, Type: "tool", ToolName: "bash", Timestamp: time.Now().Add(time.Second), ToolDuration: time.Second, ToolSuccess: true},
	}
	tree1 := BuildSpanTree("session-x", events)
	tree2 := BuildSpanTree("session-x", events)
	if tree1.SpanID != tree2.SpanID {
		t.Error("span IDs should be deterministic for same inputs")
	}
	if tree1.Children[0].SpanID != tree2.Children[0].SpanID {
		t.Error("child span IDs should be deterministic")
	}
}

func TestCountSpans(t *testing.T) {
	tree := SpanNode{
		SpanID: "root",
		Kind:   "session",
		Children: []SpanNode{
			{SpanID: "t1", Kind: "turn", Children: []SpanNode{
				{SpanID: "l1", Kind: "llm"},
				{SpanID: "s1", Kind: "tool"},
			}},
		},
	}
	if n := CountSpans(tree); n != 4 {
		t.Errorf("expected 4 spans, got %d", n)
	}
}

func TestFlattenSpans(t *testing.T) {
	tree := SpanNode{
		SpanID: "root",
		Kind:   "session",
		Children: []SpanNode{
			{SpanID: "t1", Kind: "turn"},
			{SpanID: "t2", Kind: "turn", Children: []SpanNode{
				{SpanID: "l1", Kind: "llm"},
			}},
		},
	}
	flat := FlattenSpans(tree)
	if len(flat) != 4 {
		t.Errorf("expected 4 flattened spans, got %d", len(flat))
	}
	if flat[0].SpanID != "root" {
		t.Errorf("expected root first, got %q", flat[0].SpanID)
	}
}

func TestFindErrorSpans(t *testing.T) {
	tree := SpanNode{
		SpanID: "root",
		Kind:   "session",
		Children: []SpanNode{
			{SpanID: "t1", Kind: "turn", Children: []SpanNode{
				{SpanID: "ok", Kind: "tool", Attributes: map[string]interface{}{"success": true}},
				{SpanID: "fail1", Kind: "tool", Attributes: map[string]interface{}{"success": false, "error": "boom"}},
				{SpanID: "fail2", Kind: "tool", Attributes: map[string]interface{}{"success": true, "error": "warn"}},
			}},
		},
	}
	errors := FindErrorSpans(tree)
	if len(errors) != 2 {
		t.Fatalf("expected 2 error spans, got %d", len(errors))
	}
}

func TestSpanNodeDuration(t *testing.T) {
	start := time.Now()
	end := start.Add(5 * time.Second)
	span := SpanNode{StartTime: start, EndTime: end}
	if span.Duration() != 5*time.Second {
		t.Errorf("expected 5s duration, got %v", span.Duration())
	}
}

func TestExportTrace_IncludesSpanTree(t *testing.T) {
	events := []MetricEvent{
		{TurnIndex: 1, Type: "llm", Timestamp: time.Now(), Duration: 2 * time.Second, Model: "m1"},
		{TurnIndex: 1, Type: "tool", ToolName: "edit_file", Timestamp: time.Now().Add(time.Second),
			ToolDuration: 500 * time.Millisecond, ToolSuccess: true},
	}
	data, err := ExportTrace("sess", "v", "e", "m", time.Now(), events)
	if err != nil {
		t.Fatalf("ExportTrace error: %v", err)
	}
	var doc TraceDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc.SpanTree.Kind != "session" {
		t.Errorf("expected span_tree root kind 'session', got %q", doc.SpanTree.Kind)
	}
	if len(doc.SpanTree.Children) != 1 {
		t.Fatalf("expected 1 turn span, got %d", len(doc.SpanTree.Children))
	}
	if len(doc.Waterfall) != 1 {
		t.Errorf("expected 1 waterfall analysis, got %d", len(doc.Waterfall))
	}
}
