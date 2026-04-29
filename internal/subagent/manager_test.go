package subagent

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestManager_RootContext(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	ctx := m.RootContext()
	if ctx == nil {
		t.Error("expected non-nil context")
	}
}

func TestManager_Shutdown(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	m.Shutdown()
	m.Shutdown() // double shutdown should not panic
}

func TestManager_Snapshot(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	snap, ok := m.Snapshot("nonexistent")
	if ok {
		t.Error("expected false for nonexistent")
	}
	_ = snap
}

func TestManager_Cancel_Nonexistent(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	ok := m.Cancel("nonexistent-id")
	if ok {
		t.Error("expected false for nonexistent task")
	}
}

func TestManager_GetTaskOutput_Nonexistent(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	_, ok := m.GetTaskOutput("nonexistent-id")
	if ok {
		t.Error("expected false for nonexistent task")
	}
}

func TestManager_SetOnComplete(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	m.SetOnComplete(func(sa *SubAgent) {})
}

func TestManager_SetOnUpdate(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	m.SetOnUpdate(func(sa *SubAgent) {})
}

func TestManager_Notify(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	m.Notify("test-event")
}

func TestManager_SendToAgent_NoSession(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	err := m.SendToAgent("nonexistent", AgentMessage{From: "test", Message: "hello"})
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestManager_Broadcast(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	ids := m.Broadcast(AgentMessage{From: "test", Message: "hello"})
	_ = ids
}

func TestManager_ShowOutput(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	_ = m.ShowOutput()
}

func TestManager_AcquireSemaphore(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	err := m.AcquireSemaphore(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubagentToolProgressSummary(t *testing.T) {
	got := subagentToolProgressSummary("", "")
	if got != "" {
		t.Errorf("expected empty for empty input, got %q", got)
	}
}

func TestCompactProgressSummary(t *testing.T) {
	got := compactProgressSummary("")
	if got != "" {
		t.Errorf("expected empty for empty input, got %q", got)
	}
}
