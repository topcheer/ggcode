package metrics

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExportTrace_ValidJSON(t *testing.T) {
	events := []MetricEvent{
		{TurnIndex: 1, Type: "llm", TTFT: 800 * time.Millisecond, Duration: 5 * time.Second, InputTokens: 1000, OutputTokens: 200, Model: "test-model"},
		{TurnIndex: 1, Type: "tool", ToolName: "read_file", ToolSuccess: true, ToolDuration: 100 * time.Millisecond},
		{TurnIndex: 2, Type: "llm", TTFT: 600 * time.Millisecond, Duration: 3 * time.Second, InputTokens: 1500, OutputTokens: 300},
	}

	data, err := ExportTrace("session-123", "testvendor", "testendpoint", "test-model", time.Now(), events)
	if err != nil {
		t.Fatalf("ExportTrace returned error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("ExportTrace returned empty data")
	}

	// Verify it's valid JSON by unmarshalling.
	var doc TraceDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("ExportTrace produced invalid JSON: %v", err)
	}

	if doc.SessionID != "session-123" {
		t.Errorf("expected session_id 'session-123', got %q", doc.SessionID)
	}
	if doc.Vendor != "testvendor" {
		t.Errorf("expected vendor 'testvendor', got %q", doc.Vendor)
	}
	if len(doc.Events) != 3 {
		t.Errorf("expected 3 events, got %d", len(doc.Events))
	}
	if !doc.Summary.HasData() {
		t.Error("expected summary to have data")
	}
	if doc.Summary.TurnCount != 2 {
		t.Errorf("expected 2 turns in summary, got %d", doc.Summary.TurnCount)
	}
	if doc.Summary.LLMCallCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", doc.Summary.LLMCallCount)
	}
	if doc.Summary.ToolCallCount != 1 {
		t.Errorf("expected 1 tool call, got %d", doc.Summary.ToolCallCount)
	}
	if doc.ExportedAt.IsZero() {
		t.Error("expected exported_at to be set")
	}
}

func TestExportTrace_EmptyEvents(t *testing.T) {
	data, err := ExportTrace("empty-session", "", "", "", time.Time{}, nil)
	if err != nil {
		t.Fatalf("ExportTrace returned error: %v", err)
	}

	var doc TraceDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("ExportTrace produced invalid JSON: %v", err)
	}

	if doc.Summary.HasData() {
		t.Error("expected empty summary for no events")
	}
	if len(doc.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(doc.Events))
	}
}

func TestExportTrace_IncludesToolSummaries(t *testing.T) {
	events := []MetricEvent{
		{TurnIndex: 1, Type: "tool", ToolName: "read_file", ToolSuccess: true, ToolDuration: 200 * time.Millisecond},
		{TurnIndex: 1, Type: "tool", ToolName: "read_file", ToolSuccess: true, ToolDuration: 400 * time.Millisecond},
		{TurnIndex: 1, Type: "tool", ToolName: "read_file", ToolSuccess: false, ToolError: "not found", ToolDuration: 100 * time.Millisecond},
		{TurnIndex: 1, Type: "tool", ToolName: "grep", ToolSuccess: true, ToolDuration: 50 * time.Millisecond},
	}

	data, err := ExportTrace("s1", "v", "e", "m", time.Now(), events)
	if err != nil {
		t.Fatalf("ExportTrace returned error: %v", err)
	}

	var doc TraceDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should have 2 tool summaries (read_file, grep)
	if len(doc.Summary.SlowTools) != 2 {
		t.Fatalf("expected 2 tool summaries, got %d", len(doc.Summary.SlowTools))
	}

	// Find read_file summary
	var readFileSummary *ToolSummary
	for i := range doc.Summary.SlowTools {
		if doc.Summary.SlowTools[i].Name == "read_file" {
			readFileSummary = &doc.Summary.SlowTools[i]
		}
	}
	if readFileSummary == nil {
		t.Fatal("read_file not found in tool summaries")
	}
	if readFileSummary.Calls != 3 {
		t.Errorf("expected 3 calls for read_file, got %d", readFileSummary.Calls)
	}
	if readFileSummary.Failures != 1 {
		t.Errorf("expected 1 failure for read_file, got %d", readFileSummary.Failures)
	}
}
