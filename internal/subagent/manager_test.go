package subagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestManagerSpawnAndGet(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{
		MaxConcurrent: 3,
		Timeout:       30 * time.Second,
	})

	id := mgr.Spawn("test task", nil, context.Background())
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	sa, ok := mgr.Get(id)
	if !ok {
		t.Fatal("expected to find agent")
	}
	if sa.Task != "test task" {
		t.Errorf("expected task 'test task', got %q", sa.Task)
	}
	if sa.Status != StatusPending {
		t.Errorf("expected status pending, got %s", sa.Status)
	}
}

func TestManagerList(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})

	mgr.Spawn("task1", nil, context.Background())
	mgr.Spawn("task2", nil, context.Background())

	agents := mgr.List()
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

func TestManagerRunningCount(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})

	id := mgr.Spawn("task1", nil, context.Background())
	mgr.Complete(id, "done", nil)

	id2 := mgr.Spawn("task2", nil, context.Background())
	// Manually set to running
	mgr.SetCancel(id2, func() {})

	if mgr.RunningCount() != 0 {
		t.Errorf("expected 0 running, got %d", mgr.RunningCount())
	}
}

func TestManagerCancel(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	id := mgr.Spawn("task", nil, ctx)

	// Set the cancel func and mark as running manually
	mgr.SetCancel(id, cancel)
	sa, _ := mgr.Get(id)
	sa.mu.Lock()
	sa.Status = StatusRunning
	sa.mu.Unlock()

	if !mgr.Cancel(id) {
		t.Fatal("expected cancel to succeed")
	}

	sa, _ = mgr.Get(id)
	if sa.Status != StatusCancelled {
		t.Errorf("expected cancelled, got %s", sa.Status)
	}
}

func TestManagerConcurrentSpawn(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})

	// Spawn all agents first
	ids := make([]string, 10)
	for i := 0; i < 10; i++ {
		ids[i] = mgr.Spawn("task", nil, context.Background())
	}
	if len(mgr.List()) != 10 {
		t.Fatalf("expected 10 agents after spawn, got %d", len(mgr.List()))
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			mgr.Complete(id, "", nil)
		}(id)
	}
	wg.Wait()
}

func TestManagerDefaultConfig(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	if mgr.Timeout() != 5*time.Minute {
		t.Errorf("expected default timeout 5m, got %v", mgr.Timeout())
	}
}
