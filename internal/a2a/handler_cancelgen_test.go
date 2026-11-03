package a2a

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestCleanupCancelIf_GenerationMismatch(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)

	handler.mu.Lock()
	oldCtx, oldCancel := context.WithCancel(context.Background())
	_ = oldCtx
	oldGen := handler.installCancelLocked("task-A", oldCancel)

	old, ok := handler.cancels["task-A"]
	if !ok {
		handler.mu.Unlock()
		t.Fatal("expected old cancel entry")
	}
	old.cancel()
	newCtx, newCancel := context.WithCancel(context.Background())
	newGen := handler.installCancelLocked("task-A", newCancel)
	handler.mu.Unlock()

	if oldGen == newGen {
		t.Fatalf("generations must differ: %d == %d", oldGen, newGen)
	}

	handler.cleanupCancelIf("task-A", oldGen)

	handler.mu.Lock()
	entry, ok := handler.cancels["task-A"]
	handler.mu.Unlock()
	if !ok {
		t.Fatal("new cancel entry must survive stale cleanup (issue #327: resumed task became uncancelable)")
	}
	if entry.gen != newGen {
		t.Fatalf("map entry generation %d, want new gen %d", entry.gen, newGen)
	}
	if err := newCtx.Err(); err != nil {
		t.Fatalf("resumed task context must not be canceled by stale cleanup, got: %v", err)
	}

	handler.cleanupCancelIf("task-A", newGen)
	handler.mu.Lock()
	_, ok = handler.cancels["task-A"]
	handler.mu.Unlock()
	if ok {
		t.Fatal("own cleanup must remove the map entry")
	}
	if err := newCtx.Err(); err == nil {
		t.Fatal("own cleanup must call its cancel")
	}
}

func TestCleanupCancelIf_ReflectPointerAlwaysEqual(t *testing.T) {
	_, c1 := context.WithCancel(context.Background())
	_, c2 := context.WithTimeout(context.Background(), time.Minute)
	if reflect.ValueOf(c1).Pointer() != reflect.ValueOf(c2).Pointer() {
		t.Log("pointer comparison unexpectedly distinguished cancels (environment-specific); generation check still applies")
	}
}

func TestContinueTaskResumeSurvivesStaleCleanup(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)

	handler.mu.Lock()
	task := &Task{
		ID:        "task-race",
		ContextID: "ctx-race",
		Status:    TaskStatus{State: TaskStateInputRequired, Timestamp: time.Now()},
		Skill:     SkillFullTask,
		History:   []Message{{Role: "user", Parts: []Part{{Kind: "text", Text: "hi"}}}},
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}
	handler.tasks[task.ID] = task
	_, oldCancel := context.WithTimeout(context.Background(), handler.timeout)
	oldGen := handler.installCancelLocked(task.ID, oldCancel)
	handler.mu.Unlock()

	if _, err := handler.continueTask(context.Background(), task.ID, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "resume"}},
	}); err != nil {
		t.Fatal(err)
	}

	var parkedSurvivalCheck bool
	observeDeadline := time.Now().Add(5 * time.Second)
	for {
		handler.mu.Lock()
		entry, hasCancel := handler.cancels[task.ID]
		state := handler.tasks[task.ID].Status.State
		handler.mu.Unlock()

		if state == TaskStateCanceled {
			t.Fatal("resumed task must not be marked canceled by stale goroutine")
		}
		if hasCancel && entry.gen == oldGen {
			t.Fatal("map entry must belong to the resumed run, not the stale goroutine")
		}
		if hasCancel && !state.IsTerminal() {
			parkedSurvivalCheck = true
			break
		}
		if !hasCancel && state.IsTerminal() {
			break
		}
		if time.Now().After(observeDeadline) {
			t.Fatalf("resumed run neither parked nor terminal within deadline (state=%s, hasCancel=%v)", state, hasCancel)
		}
		time.Sleep(5 * time.Millisecond)
	}

	handler.cleanupCancelIf(task.ID, oldGen)

	handler.mu.Lock()
	state := handler.tasks[task.ID].Status.State
	handler.mu.Unlock()
	if state == TaskStateCanceled {
		t.Fatal("resumed task must not be marked canceled by stale goroutine")
	}
	if parkedSurvivalCheck {
		handler.mu.Lock()
		entry, hasCancel := handler.cancels[task.ID]
		handler.mu.Unlock()
		if hasCancel && entry.gen == oldGen {
			t.Fatal("stale cleanup must never take ownership of the live entry")
		}
	}

	handler.mu.Lock()
	if e, ok := handler.cancels[task.ID]; ok {
		e.cancel()
		delete(handler.cancels, task.ID)
	}
	handler.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		handler.mu.Lock()
		s := handler.tasks[task.ID].Status.State
		handler.mu.Unlock()
		if s.IsTerminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCancelEntryGenerationMonotonic(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	handler.mu.Lock()
	g1 := handler.installCancelLocked("t1", func() {})
	g2 := handler.installCancelLocked("t2", func() {})
	handler.mu.Unlock()
	if g1 == g2 {
		t.Fatalf("generations must be unique across installs: %d == %d", g1, g2)
	}
}
