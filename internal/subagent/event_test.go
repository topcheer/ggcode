package subagent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestAppendEventRecordsText(t *testing.T) {
	sa := &SubAgent{ID: "sa-1", Status: StatusRunning}
	sa.appendEvent(AgentEvent{Type: AgentEventText, Text: "hello"})
	events := sa.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != AgentEventText || events[0].Text != "hello" {
		t.Fatalf("unexpected event: %+v", events[0])
	}
}

func TestAppendEventRecordsToolCall(t *testing.T) {
	sa := &SubAgent{ID: "sa-1", Status: StatusRunning}
	sa.appendEvent(AgentEvent{
		Type:     AgentEventToolCall,
		ToolName: "read_file",
		ToolArgs: `{"path":"/tmp/test.go"}`,
	})
	events := sa.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ToolName != "read_file" {
		t.Fatalf("expected tool name read_file, got %s", events[0].ToolName)
	}
}

func TestAppendEventRecordsToolResult(t *testing.T) {
	sa := &SubAgent{ID: "sa-1", Status: StatusRunning}
	sa.appendEvent(AgentEvent{
		Type:     AgentEventToolResult,
		ToolName: "read_file",
		Result:   "file contents here",
		IsError:  false,
	})
	events := sa.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].IsError {
		t.Fatal("expected IsError=false")
	}
}

func TestAppendEventRecordsError(t *testing.T) {
	sa := &SubAgent{ID: "sa-1", Status: StatusRunning}
	sa.appendEvent(AgentEvent{
		Type:    AgentEventError,
		Text:    "something went wrong",
		IsError: true,
	})
	events := sa.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].IsError {
		t.Fatal("expected IsError=true")
	}
}

func TestAppendEventCapsAtMax(t *testing.T) {
	sa := &SubAgent{ID: "sa-1", Status: StatusRunning}
	for i := 0; i < maxAgentEvents+50; i++ {
		sa.appendEvent(AgentEvent{Type: AgentEventText, Text: "line"})
	}
	events := sa.Events()
	if len(events) != maxAgentEvents {
		t.Fatalf("expected %d events (capped), got %d", maxAgentEvents, len(events))
	}
	// First events should be dropped (FIFO)
	// The oldest remaining should be the 51st appended event
}

func TestEventsReturnsCopy(t *testing.T) {
	sa := &SubAgent{ID: "sa-1", Status: StatusRunning}
	sa.appendEvent(AgentEvent{Type: AgentEventText, Text: "original"})
	events := sa.Events()
	events[0].Text = "modified"
	// Original should be unchanged
	original := sa.Events()
	if original[0].Text != "original" {
		t.Fatal("Events() should return a copy, but modification affected internal state")
	}
}

func TestSnapshotIncludesEvents(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("task", "display task", nil, nil)
	sa, _ := mgr.Get(id)
	sa.appendEvent(AgentEvent{Type: AgentEventText, Text: "hello"})
	sa.appendEvent(AgentEvent{Type: AgentEventToolCall, ToolName: "read_file", ToolArgs: `{}`})
	sa.appendEvent(AgentEvent{Type: AgentEventToolResult, ToolName: "read_file", Result: "contents"})

	snap, ok := mgr.Snapshot(id)
	if !ok {
		t.Fatal("expected snapshot")
	}
	if len(snap.Events) != 3 {
		t.Fatalf("expected 3 events in snapshot, got %d", len(snap.Events))
	}
	if snap.Events[0].Type != AgentEventText {
		t.Fatalf("expected first event to be Text, got %d", snap.Events[0].Type)
	}
	if snap.Events[1].ToolName != "read_file" {
		t.Fatalf("expected second event tool name read_file, got %s", snap.Events[1].ToolName)
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exact10!", 8, "exact10!"},
		{"this is a longer string", 10, "this is a ..."},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncateStr(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
