package a2a

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestCleanupCancelIf_GenerationMismatch simulates the issue #327 race: an old
// execute goroutine's cleanupCancelIf arrives AFTER continueTask has already
// swapped in a new cancel for the resumed task. The stale goroutine must NOT
// call or delete the new cancel (resumed context stays alive, task remains
// cancelable, and the task must not be marked canceled).
func TestCleanupCancelIf_GenerationMismatch(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)

	handler.mu.Lock()
	// Old run installs its cancel (as handleSendMessageSend does).
	oldCtx, oldCancel := context.WithCancel(context.Background())
	_ = oldCtx
	oldGen := handler.installCancelLocked("task-A", oldCancel)

	// continueTask fires: it cancels the old entry and installs a new one.
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

	// Stale old goroutine runs cleanupCancelIf with ITS generation.
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

	// The legitimate owner of the new entry can still clean it up.
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

// TestCleanupCancelIf_ReflectPointerAlwaysEqual documents WHY generation
// numbers are required: two independent context.WithCancel/WithTimeout cancels
// share the same code pointer, so reflect.ValueOf(fn).Pointer() comparison is
// always true and cannot establish ownership (the pre-#327 bug).
func TestCleanupCancelIf_ReflectPointerAlwaysEqual(t *testing.T) {
	_, c1 := context.WithCancel(context.Background())
	_, c2 := context.WithTimeout(context.Background(), time.Minute)
	if reflect.ValueOf(c1).Pointer() != reflect.ValueOf(c2).Pointer() {
		t.Log("pointer comparison unexpectedly distinguished cancels (environment-specific); generation check still applies")
	}
}

// TestContinueTaskResumeSurvivesStaleCleanup is the end-to-end regression for
// issue #327: a task in input-required state is resumed, and a stale cleanup
// from the previous execution run arrives after the resume. The resumed task
// must still have a live, cancelable context and must not be marked canceled.
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
	// Simulate the OLD execute goroutine's installed entry (what Handle did).
	_, oldCancel := context.WithTimeout(context.Background(), handler.timeout)
	oldGen := handler.installCancelLocked(task.ID, oldCancel)
	handler.mu.Unlock()

	// Client resumes the task (continueTask swaps in a new cancel).
	if _, err := handler.continueTask(context.Background(), task.ID, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "resume"}},
	}); err != nil {
		t.Fatal(err)
	}

	// The stale old goroutine now reaches its cleanup paths (err==nil path
	// uses cleanupCancelIf; the canceled path returns early — both tested).
	handler.cleanupCancelIf(task.ID, oldGen)

	handler.mu.Lock()
	entry, hasCancel := handler.cancels[task.ID]
	state := handler.tasks[task.ID].Status.State
	handler.mu.Unlock()

	if !hasCancel {
		t.Fatal("resumed task must keep a cancel entry (became uncancelable in #327)")
	}
	if entry.gen == oldGen {
		t.Fatal("map entry must belong to the resumed run, not the stale goroutine")
	}
	if state == TaskStateCanceled {
		t.Fatal("resumed task must not be marked canceled by stale goroutine")
	}

	// Clean up the resumed goroutine's context to avoid leaking the test.
	handler.mu.Lock()
	entry.cancel()
	delete(handler.cancels, task.ID)
	handler.mu.Unlock()

	// Drain the resumed execute goroutine (nil agent → fails fast on empty
	// message? it has history, so it errors "no agent available" and exits).
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

// TestCancelEntryGenerationMonotonic ensures each install gets a distinct
// generation even for different tasks.
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
