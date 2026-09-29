package a2a

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
)

// zz_issue2896_test.go - regression probe for #2896: a follow-up re-sent
// with the same MessageID (client timeout + retry) must NOT append the
// message twice or execute the side effect twice. The new-task path has
// had MessageID idempotency since #565 G; the continueTask path is the
// symmetric gap.

func newInputRequiredTask(h *TaskHandler, taskID string) *Task {
	tk := &Task{
		ID:        taskID,
		Skill:     SkillCodeEdit,
		ContextID: "ctx",
		History:   []Message{{MessageID: "initial", Parts: []Part{{Kind: "text", Text: "edit main.go"}}}},
		Status:    TaskStatus{State: TaskStateInputRequired, Timestamp: time.Now()},
		UpdatedAt: time.Now(),
	}
	h.mu.Lock()
	h.tasks[taskID] = tk
	h.mu.Unlock()
	return tk
}

func newIssue2896Handler(t *testing.T) *TaskHandler {
	t.Helper()
	reg := newStubRegistry()
	a := agent.NewAgent(&scriptProvider{script: [][]provider.StreamEvent{
		{{Type: provider.StreamEventText, Text: "edit done"}},
	}}, reg, "test", 5)
	return newHandler(a, reg)
}

func TestIssue2896ContinueTaskMessageIdIdempotency(t *testing.T) {
	h := newIssue2896Handler(t)
	taskID := "task-2896"
	task := newInputRequiredTask(h, taskID)

	follow := Message{MessageID: "follow-1", Parts: []Part{{Kind: "text", Text: "yes, proceed"}}}

	// First continue: consumes the message, resumes execution.
	snap1, err := h.continueTask(context.Background(), taskID, follow)
	if err != nil {
		t.Fatalf("first continueTask: %v", err)
	}
	h.mu.Lock()
	histLen := len(task.History)
	state := task.Status.State
	h.mu.Unlock()
	if histLen != 2 {
		t.Fatalf("expected history length 2 after first continue, got %d", histLen)
	}
	if state != TaskStateWorking {
		t.Fatalf("expected working state after continue, got %s", state)
	}

	// Retry with the SAME MessageID (client timeout + retry): must return
	// the mapped snapshot WITHOUT appending or re-executing.
	snap2, err := h.continueTask(context.Background(), taskID, follow)
	if err != nil {
		t.Fatalf("retry continueTask: %v", err)
	}
	h.mu.Lock()
	histLen = len(task.History)
	h.mu.Unlock()
	if histLen != 2 {
		t.Fatalf("#2896: retry with same MessageID appended again (history=%d) - side effect would double-execute", histLen)
	}
	if snap1.ID != snap2.ID {
		t.Fatalf("retry must return the same task, got %q vs %q", snap1.ID, snap2.ID)
	}

	// A DIFFERENT MessageID is a genuinely new follow-up and appends fresh.
	follow2 := Message{MessageID: "follow-2", Parts: []Part{{Kind: "text", Text: "another"}}}
	h.mu.Lock()
	task.Status = TaskStatus{State: TaskStateInputRequired, Timestamp: time.Now()}
	h.mu.Unlock()
	if _, err := h.continueTask(context.Background(), taskID, follow2); err != nil {
		t.Fatalf("second distinct follow-up: %v", err)
	}
	h.mu.Lock()
	histLen = len(task.History)
	h.mu.Unlock()
	if histLen != 3 {
		t.Fatalf("distinct MessageID follow-up must append, history=%d", histLen)
	}
}

func TestIssue2896ContinueTaskMessageIdCrossTaskRejected(t *testing.T) {
	h := newIssue2896Handler(t)
	newInputRequiredTask(h, "task-A")
	newInputRequiredTask(h, "task-B")

	follow := Message{MessageID: "shared-1", Parts: []Part{{Kind: "text", Text: "yes"}}}
	if _, err := h.continueTask(context.Background(), "task-A", follow); err != nil {
		t.Fatalf("first continue on task-A: %v", err)
	}
	// Same MessageID aimed at task-B must be rejected, not silently
	// double-mapped (a client bug should surface, not corrupt state).
	_, err := h.continueTask(context.Background(), "task-B", follow)
	if err == nil {
		t.Fatal("#2896: same MessageID mapped to a different task must be rejected")
	}
	if !strings.Contains(err.Error(), "different task") {
		t.Fatalf("expected cross-task rejection error, got: %v", err)
	}
}
