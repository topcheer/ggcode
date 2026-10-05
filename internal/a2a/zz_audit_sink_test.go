package a2a

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/audit"
)

// r33: inbound remote A2A handoffs must leave a non-repudiable audit record.
// Tasks submitted by peer agents execute locally with the caller's tool
// permissions - previously zero audit trace on the inbound path.

// TestAuditSinkSealsReceivedAndTerminalEvents runs a task to a terminal state
// and asserts the governance ledger received both lifecycle events.
func TestAuditSinkSealsReceivedAndTerminalEvents(t *testing.T) {
	var mu sync.Mutex
	var events []audit.Event
	h := NewTaskHandler(stubWorkspace(), nil, newStubRegistry(),
		WithMaxTasks(5), WithTimeout(5*time.Second),
		WithAuditSink(func(ev audit.Event) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}),
	)

	// file-search with a nil agent: Handle accepts the skill, execute
	// starts (received event) and fails at "agent required" (terminal
	// event) - full lifecycle without mocking an LLM.
	task, err := h.Handle(context.Background(), SkillFileSearch, Message{MessageID: "audit-m1", Role: "user", Parts: []Part{{Kind: "text", Text: "find TODOs"}}}, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// execute runs in a goroutine; poll for the terminal audit event.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) < 2 {
		t.Fatalf("audit sink got %d events, want >= 2 (received + terminal)", len(events))
	}
	if events[0].Tool != "a2a.task.received" || events[0].Status != audit.StatusOK {
		t.Errorf("first event = %s/%s, want a2a.task.received/%s", events[0].Tool, events[0].Status, audit.StatusOK)
	}
	last := events[len(events)-1]
	if last.Tool != "a2a.task.failed" || last.Status != audit.StatusError {
		t.Errorf("terminal event = %s/%s, want a2a.task.failed/%s", last.Tool, last.Status, audit.StatusError)
	}
	if last.TaskID != task.ID {
		t.Errorf("terminal event TaskID = %q, want task %q", last.TaskID, task.ID)
	}
	if last.Session != task.ID {
		t.Errorf("terminal event Session = %q, want task ID %q (ledger session ties to the task lifecycle)", last.Session, task.ID)
	}
}

// TestAuditSinkSealsErrSummaryCap asserts the error summary is truncated to
// auditErrMax before sealing (governance ledgers prove what ran without
// duplicating large payloads) and that the configured sink is invoked.
func TestAuditSinkSealsErrSummaryCap(t *testing.T) {
	h := NewTaskHandler(stubWorkspace(), nil, newStubRegistry(),
		WithMaxTasks(5), WithTimeout(5*time.Second))
	var got audit.Event
	done := make(chan struct{})
	h.mu.Lock()
	h.auditSink = func(ev audit.Event) { got = ev; close(done) }
	fake := &Task{ID: "cap-task", Skill: "bogus"}
	h.mu.Unlock()

	long := make([]byte, auditErrMax+50)
	for i := range long {
		long[i] = 'x'
	}
	h.auditTaskEvent(fake, "a2a.task.failed", audit.StatusError, string(long))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("auditTaskEvent must invoke the configured sink")
	}
	if len(got.Err) != auditErrMax {
		t.Errorf("err summary length = %d, want capped at %d", len(got.Err), auditErrMax)
	}
}

// TestAuditSinkNilIsInert asserts handlers without a sink skip audit work
// entirely (default configuration is unchanged, zero overhead).
func TestAuditSinkNilIsInert(t *testing.T) {
	h := NewTaskHandler(stubWorkspace(), nil, newStubRegistry(),
		WithMaxTasks(5), WithTimeout(5*time.Second))
	h.mu.Lock()
	fake := &Task{ID: "t", Skill: "bogus"}
	h.auditSink = nil
	h.mu.Unlock()
	h.auditTaskEvent(fake, "a2a.task.received", audit.StatusOK, "") // must not panic
}
