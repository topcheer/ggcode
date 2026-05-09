package subagent

import (
	"context"
	"testing"

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
