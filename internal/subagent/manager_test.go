package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func newTestManager() *Manager {
	return NewManager(config.SubAgentConfig{})
}

func TestManager_List(t *testing.T) {
	m := newTestManager()
	list := m.List()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestManager_RunningCount(t *testing.T) {
	m := newTestManager()
	if m.RunningCount() != 0 {
		t.Errorf("expected 0 running, got %d", m.RunningCount())
	}
}

func TestManager_SetCancel(t *testing.T) {
	m := newTestManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id := m.Spawn("test", "test", "do something", nil, ctx)
	m.SetCancel(id, cancel)

	sa, ok := m.Get(id)
	if !ok {
		t.Fatal("expected agent to exist")
	}
	if sa.Status != StatusRunning {
		t.Errorf("expected running, got %s", sa.Status)
	}
}

func TestManager_Complete(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	id := m.Spawn("test", "test", "do something", nil, ctx)
	m.Complete(id, "done", nil)

	sa, ok := m.Get(id)
	if !ok {
		t.Fatal("expected agent")
	}
	if sa.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", sa.Status)
	}
	if sa.Result != "done" {
		t.Errorf("expected result 'done', got %q", sa.Result)
	}
}

func TestManager_Complete_WithError(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	id := m.Spawn("test", "test", "do something", nil, ctx)
	m.Complete(id, "", context.Canceled)

	sa, _ := m.Get(id)
	if sa.Status != StatusFailed {
		t.Errorf("expected failed, got %s", sa.Status)
	}
	if sa.Error == nil {
		t.Error("expected error")
	}
}

func TestManager_Complete_WithCallback(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	completed := false
	m.SetOnComplete(func(sa *SubAgent) {
		completed = true
	})

	id := m.Spawn("test", "test", "do something", nil, ctx)
	m.Complete(id, "ok", nil)

	if !completed {
		t.Error("expected onComplete callback")
	}
}

func TestManager_Complete_NotFound(t *testing.T) {
	m := newTestManager()
	m.Complete("nonexistent", "", nil) // should not panic
}

func TestManager_UpdateProgress(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	id := m.Spawn("test", "test", "do something", nil, ctx)
	m.UpdateProgress(id, "halfway done")

	sa, _ := m.Get(id)
	if sa.ProgressSummary != "halfway done" {
		t.Errorf("expected 'halfway done', got %q", sa.ProgressSummary)
	}
}

func TestManager_UpdateProgress_NotFound(t *testing.T) {
	m := newTestManager()
	m.UpdateProgress("nonexistent", "summary") // should not panic
}

func TestManager_UpdateActivity(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	id := m.Spawn("test", "test", "do something", nil, ctx)
	m.UpdateActivity(id, "thinking", "read_file", "/tmp/test.go")

	sa, _ := m.Get(id)
	if sa.CurrentPhase != "thinking" {
		t.Errorf("expected 'thinking', got %q", sa.CurrentPhase)
	}
	if sa.CurrentTool != "read_file" {
		t.Errorf("expected 'read_file', got %q", sa.CurrentTool)
	}
}

func TestManager_UpdateActivity_WithCallback(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	updated := false
	m.SetOnUpdate(func(sa *SubAgent) {
		updated = true
	})

	id := m.Spawn("test", "test", "do something", nil, ctx)
	m.UpdateActivity(id, "writing", "write_file", "/tmp/out.go")

	if !updated {
		t.Error("expected onUpdate callback")
	}
}

func TestManager_ReleaseSemaphore(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	err := m.AcquireSemaphore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m.ReleaseSemaphore() // should not block or panic
}

func TestManager_Timeout(t *testing.T) {
	m := newTestManager()
	timeout := m.Timeout()
	if timeout == 0 {
		t.Error("expected non-zero timeout")
	}
}

func TestManager_Cancel_RunningAgent(t *testing.T) {
	m := newTestManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id := m.Spawn("test", "test", "do something", nil, ctx)
	m.SetCancel(id, cancel)

	ok := m.Cancel(id)
	if !ok {
		t.Error("expected cancel to succeed")
	}

	sa, _ := m.Get(id)
	if sa.Status != StatusCancelled {
		t.Errorf("expected cancelled, got %s", sa.Status)
	}
}

func TestManager_Cancel_AlreadyDone(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	id := m.Spawn("test", "test", "do something", nil, ctx)
	m.Complete(id, "done", nil)

	ok := m.Cancel(id)
	if ok {
		t.Error("expected cancel to fail for completed agent")
	}
}

func TestManager_CancelAll(t *testing.T) {
	m := newTestManager()
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()

	id1 := m.Spawn("a1", "a1", "task1", nil, ctx1)
	id2 := m.Spawn("a2", "a2", "task2", nil, ctx2)
	id3 := m.Spawn("a3", "a3", "task3", nil, ctx3)

	// Set two as running
	m.SetCancel(id1, cancel1)
	m.SetCancel(id2, cancel2)
	// id3 stays pending

	cancelled := m.CancelAll()
	if cancelled != 2 {
		t.Fatalf("expected 2 cancelled, got %d", cancelled)
	}

	sa1, _ := m.Get(id1)
	sa2, _ := m.Get(id2)
	sa3, _ := m.Get(id3)

	if sa1.Status != StatusCancelled {
		t.Errorf("expected a1 cancelled, got %s", sa1.Status)
	}
	if sa2.Status != StatusCancelled {
		t.Errorf("expected a2 cancelled, got %s", sa2.Status)
	}
	if sa3.Status == StatusCancelled {
		t.Errorf("expected a3 NOT cancelled (was pending), got %s", sa3.Status)
	}
}

func TestManager_CancelAll_Empty(t *testing.T) {
	m := newTestManager()
	cancelled := m.CancelAll()
	if cancelled != 0 {
		t.Errorf("expected 0 cancelled for empty manager, got %d", cancelled)
	}
}

func TestSubAgent_statusInfo(t *testing.T) {
	sa := &SubAgent{
		ID:           "agent-1",
		Name:         "researcher",
		Status:       StatusRunning,
		CurrentPhase: "reading files",
		EndedAt:      time.Time{}, // zero
		Mailbox:      make(chan AgentMessage, 16),
	}

	info := sa.statusInfo()
	if info.ID != "agent-1" {
		t.Errorf("expected ID agent-1, got %s", info.ID)
	}
	if info.Name != "researcher" {
		t.Errorf("expected Name researcher, got %s", info.Name)
	}
	if info.Status != StatusRunning {
		t.Errorf("expected Status running, got %s", info.Status)
	}
	if info.CurrentPhase != "reading files" {
		t.Errorf("expected CurrentPhase 'reading files', got %s", info.CurrentPhase)
	}
	if !info.EndedAt.IsZero() {
		t.Errorf("expected zero EndedAt, got %v", info.EndedAt)
	}
}

func TestManager_Statuses(t *testing.T) {
	m := newTestManager()

	// Empty manager
	statuses := m.Statuses()
	if len(statuses) != 0 {
		t.Fatalf("expected 0 statuses, got %d", len(statuses))
	}

	// Spawn agents
	ctx1 := context.Background()
	ctx2 := context.Background()
	m.Spawn("alpha", "alpha", "task-a", nil, ctx1)
	m.Spawn("beta", "beta", "task-b", nil, ctx2)

	statuses = m.Statuses()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	// Verify names are present (order not guaranteed)
	names := map[string]bool{}
	for _, s := range statuses {
		names[s.Name] = true
		if s.Status == "" {
			t.Error("status should not be empty")
		}
	}
	if !names["alpha"] {
		t.Error("expected alpha agent in statuses")
	}
	if !names["beta"] {
		t.Error("expected beta agent in statuses")
	}

	// Cleanup
	m.CancelAll()
}

func TestManager_Statuses_DoesNotCopyEvents(t *testing.T) {
	// Verify Statuses() is lightweight — it should not reflect event counts.
	// We add events to an agent, call Statuses(), and verify it returns without
	// copying or exposing the events array.
	m := newTestManager()
	ctx := context.Background()
	id := m.Spawn("event-agent", "event-agent", "eventful task", nil, ctx)

	// Add events to the agent
	agents := m.List()
	for _, sa := range agents {
		if sa.ID == id {
			for i := 0; i < 50; i++ {
				sa.appendEvent(AgentEvent{Type: AgentEventText, Text: "chunk"})
			}
		}
	}

	// Statuses should still work and be lightweight
	statuses := m.Statuses()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}

	// StatusInfo has no Events field — just ID, Name, Status, CurrentPhase, EndedAt
	if statuses[0].Name != "event-agent" {
		t.Errorf("expected event-agent, got %s", statuses[0].Name)
	}

	m.CancelAll()
}

func TestSubAgent_EventsSince(t *testing.T) {
	sa := &SubAgent{
		ID:      "agent-1",
		Name:    "coder",
		Status:  StatusRunning,
		Mailbox: make(chan AgentMessage, 16),
	}

	// No events yet
	events, total := sa.EventsSince(0)
	if total != 0 {
		t.Fatalf("expected 0 total, got %d", total)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}

	// Add events
	for i := 0; i < 5; i++ {
		sa.appendEvent(AgentEvent{Type: AgentEventText, Text: "chunk"})
	}

	// Get all
	events, total = sa.EventsSince(0)
	if total != 5 {
		t.Fatalf("expected 5 total, got %d", total)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}

	// Get incremental
	events, total = sa.EventsSince(3)
	if total != 5 {
		t.Fatalf("expected 5 total, got %d", total)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 incremental events, got %d", len(events))
	}

	// fromIdx >= total returns empty
	events, total = sa.EventsSince(10)
	if total != 5 {
		t.Fatalf("expected 5 total, got %d", total)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestManager_EventsSince_Integration(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()
	id := m.Spawn("worker", "worker", "task", nil, ctx)

	// Add events via the agent
	agents := m.List()
	for _, sa := range agents {
		if sa.ID == id {
			for i := 0; i < 20; i++ {
				sa.appendEvent(AgentEvent{Type: AgentEventText, Text: "data"})
			}
		}
	}

	// Get incremental from 15
	agents = m.List()
	for _, sa := range agents {
		if sa.ID == id {
			events, total := sa.EventsSince(15)
			if total != 20 {
				t.Fatalf("expected 20 total, got %d", total)
			}
			if len(events) != 5 {
				t.Fatalf("expected 5 incremental events, got %d", len(events))
			}
		}
	}

	m.CancelAll()
}
