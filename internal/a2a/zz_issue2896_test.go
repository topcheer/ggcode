package a2a

import (
	"context"
	"testing"
	"time"
)

// TestIssue2896ContinueTaskRetryDedup: a re-sent follow-up with the SAME
// MessageID must return the task snapshot WITHOUT appending the message to
// History or spawning a second execute — code-edit/command-exec side effects
// would otherwise double-run on timeout+retry.
func TestIssue2896ContinueTaskRetryDedup(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	taskID := "task-2896"

	handler.mu.Lock()
	task := &Task{
		ID:        taskID,
		Status:    TaskStatus{State: TaskStateInputRequired, Timestamp: time.Now()},
		Skill:     SkillFileSearch,
		History:   []Message{{MessageID: "m1"}},
		CreatedAt: time.Now(),
	}
	handler.tasks[taskID] = task
	handler.mu.Unlock()

	// First follow-up (applied). Agent is nil, so execute fails fast — that
	// is fine; what matters is that History gained exactly one entry and the
	// MessageID got recorded.
	msg := Message{MessageID: "followup-1"}
	if _, err := handler.continueTask(context.Background(), taskID, msg); err != nil {
		t.Fatalf("first follow-up: unexpected error: %v", err)
	}

	// Wait for the (failing, agent==nil) execute goroutine to settle so the
	// History count below is observed deterministically after the retry.
	time.Sleep(50 * time.Millisecond)

	handler.mu.Lock()
	historyLen := len(handler.tasks[taskID].History)
	handler.mu.Unlock()
	if historyLen < 2 {
		t.Fatalf("after first follow-up: History len = %d, want >= 2 (follow-up applied)", historyLen)
	}

	// Retry with the same MessageID (the #2896 scenario). Task is now Failed
	// (agent==nil), i.e. NOT input-required — the pre-fix code would return
	// an error here, and in the input-required case it would re-execute.
	snap, err := handler.continueTask(context.Background(), taskID, msg)
	if err != nil {
		t.Fatalf("retry with same MessageID must dedup, got error: %v", err)
	}
	if snap.ID != taskID {
		t.Fatalf("dedup must return the same task, got %s", snap.ID)
	}

	handler.mu.Lock()
	historyLen2 := len(handler.tasks[taskID].History)
	handler.mu.Unlock()
	if historyLen2 != historyLen {
		t.Fatalf("retry changed History: len = %d, want unchanged %d (dedup must not re-apply)", historyLen2, historyLen)
	}
}

// TestIssue2896DistinctMessageIDsBothApply: dedup is keyed on MessageID — a
// second follow-up with a DIFFERENT MessageID in a genuine multi-turn flow
// must still be applied (regression guard against over-aggressive dedup).
func TestIssue2896DistinctMessageIDsBothApply(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	taskID := "task-2896b"

	handler.mu.Lock()
	handler.tasks[taskID] = &Task{
		ID:        taskID,
		Status:    TaskStatus{State: TaskStateInputRequired, Timestamp: time.Now()},
		Skill:     SkillFileSearch,
		History:   []Message{{MessageID: "m1"}},
		CreatedAt: time.Now(),
	}
	handler.mu.Unlock()

	if _, err := handler.continueTask(context.Background(), taskID, Message{MessageID: "f1"}); err != nil {
		t.Fatalf("first follow-up: %v", err)
	}
	// Put the task back into input-required to simulate the next round.
	handler.mu.Lock()
	handler.tasks[taskID].Status = TaskStatus{State: TaskStateInputRequired, Timestamp: time.Now()}
	handler.mu.Unlock()

	if _, err := handler.continueTask(context.Background(), taskID, Message{MessageID: "f2"}); err != nil {
		t.Fatalf("second follow-up with distinct MessageID must apply, got: %v", err)
	}

	handler.mu.Lock()
	historyLen := len(handler.tasks[taskID].History)
	handler.mu.Unlock()
	if historyLen != 3 {
		t.Fatalf("distinct MessageIDs must both apply: History len = %d, want 3", historyLen)
	}
}

// TestIssue2896StaleMappingProceeds: a MessageID mapped to a DIFFERENT task
// is a client-side ID reuse — the stale entry is dropped and the follow-up
// proceeds (mirroring the new-task path at L191).
func TestIssue2896StaleMappingProceeds(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	taskID := "task-2896c"

	handler.mu.Lock()
	handler.tasks[taskID] = &Task{
		ID:        taskID,
		Status:    TaskStatus{State: TaskStateInputRequired, Timestamp: time.Now()},
		Skill:     SkillFileSearch,
		History:   []Message{{MessageID: "m1"}},
		CreatedAt: time.Now(),
	}
	handler.messageIndex["reused-id"] = "some-other-task"
	handler.mu.Unlock()

	snap, err := handler.continueTask(context.Background(), taskID, Message{MessageID: "reused-id"})
	if err != nil {
		t.Fatalf("stale mapping must proceed, got error: %v", err)
	}
	if snap.ID != taskID {
		t.Fatalf("expected task %s, got %s", taskID, snap.ID)
	}

	handler.mu.Lock()
	mapped, ok := handler.messageIndex["reused-id"]
	historyLen := len(handler.tasks[taskID].History)
	handler.mu.Unlock()
	if !ok || mapped != taskID {
		t.Fatalf("mapping must be re-pointed to %s, got %q (ok=%v)", taskID, mapped, ok)
	}
	if historyLen != 2 {
		t.Fatalf("follow-up must be applied: History len = %d, want 2", historyLen)
	}
}
