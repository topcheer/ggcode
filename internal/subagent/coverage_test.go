package subagent

import (
	"testing"
)

func TestStatusValues(t *testing.T) {
	if StatusPending != "pending" {
		t.Errorf("StatusPending = %q", StatusPending)
	}
	if StatusRunning != "running" {
		t.Errorf("StatusRunning = %q", StatusRunning)
	}
	if StatusCompleted != "completed" {
		t.Errorf("StatusCompleted = %q", StatusCompleted)
	}
}

func TestSubAgent_RecordEvent(t *testing.T) {
	sa := &SubAgent{}
	sa.RecordEvent(AgentEvent{Type: AgentEventText, Text: "hello"})
	events := sa.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", events[0].Text)
	}
}

func TestSubAgent_IncrementToolCalls(t *testing.T) {
	sa := &SubAgent{}
	sa.IncrementToolCalls()
	sa.IncrementToolCalls()
	if sa.ToolCallCount != 2 {
		t.Errorf("expected 2 tool calls, got %d", sa.ToolCallCount)
	}
}

func TestSubAgent_SetStatus(t *testing.T) {
	sa := &SubAgent{}
	sa.setStatus(StatusRunning)
	if sa.getStatus() != StatusRunning {
		t.Errorf("expected running, got %q", sa.getStatus())
	}
}

func TestSubAgent_SetActivity(t *testing.T) {
	sa := &SubAgent{}
	sa.setActivity("executing", "edit_file", "file.go")
	if sa.CurrentPhase != "executing" {
		t.Errorf("expected 'executing', got %q", sa.CurrentPhase)
	}
}

func TestSubAgent_Snapshot(t *testing.T) {
	sa := &SubAgent{
		ID:   "test-1",
		Task: "do something",
	}
	sa.setStatus(StatusRunning)
	snap := sa.snapshot()
	if snap.ID != "test-1" {
		t.Errorf("expected 'test-1', got %q", snap.ID)
	}
	if snap.Status != StatusRunning {
		t.Errorf("expected running, got %q", snap.Status)
	}
}
