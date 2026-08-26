//go:build integration_local

package a2a

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestIssue1049_CancelTaskPushAsync verifies that CancelTask does not block
// on push notifier callback (DNS validation). The push callback should be
// invoked via safego.Go, allowing CancelTask to return immediately.
func TestIssue1049_CancelTaskPushAsync(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0, APIKey: "test-key"}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Create a task and move it to working state.
	task, _ := handler.Handle(context.Background(), SkillFullTask, Message{
		Role:  "user",
		Parts: []Part{{Kind: "text", Text: "test"}},
	}, "")
	handler.mu.Lock()
	handler.tasks[task.ID].Status = TaskStatus{State: TaskStateWorking}
	handler.mu.Unlock()

	// Simulate a slow push callback (like DNS resolution would block).
	var pushCalled bool
	var pushMu sync.Mutex
	handler.SetPushNotifier(func(taskID string, payload StreamResponse) {
		pushMu.Lock()
		defer pushMu.Unlock()
		pushCalled = true
		// Simulate blocking DNS resolution (actual push_guard uses 3s timeout).
		time.Sleep(100 * time.Millisecond)
	})

	// CancelTask should return immediately, not wait for push callback.
	start := time.Now()
	if err := handler.CancelTask(task.ID); err != nil {
		t.Fatalf("CancelTask failed: %v", err)
	}
	elapsed := time.Since(start)

	// Should complete well before the simulated 100ms delay.
	if elapsed > 50*time.Millisecond {
		t.Errorf("CancelTask took too long (%v), should return before push callback completes", elapsed)
	}

	// Wait for async push callback to fire.
	time.Sleep(200 * time.Millisecond)
	pushMu.Lock()
	if !pushCalled {
		t.Error("push callback was never called")
	}
	pushMu.Unlock()
}

// TestIssue1050_RequestInputNotifiesSubscribers verifies that RequestInput
// triggers both onTaskEvent and pushNotifier callbacks, so webhook clients
// can detect input-required state.
func TestIssue1050_RequestInputNotifiesSubscribers(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)

	var taskEventCalled bool
	var pushCalled bool
	var taskEventMu, pushMu sync.Mutex

	handler.SetOnTaskEvent(func(msg TaskEventMessage) {
		taskEventMu.Lock()
		defer taskEventMu.Unlock()
		taskEventCalled = true
		if msg.Type != "input-required" {
			t.Errorf("expected event type 'input-required', got %s", msg.Type)
		}
	})

	handler.SetPushNotifier(func(taskID string, payload StreamResponse) {
		pushMu.Lock()
		defer pushMu.Unlock()
		pushCalled = true
		if payload.StatusUpdate == nil {
			t.Error("expected StatusUpdate in push payload")
		} else if payload.StatusUpdate.Final {
			t.Error("input-required should not be marked Final")
		}
	})

	// Create a task and move it to working state.
	task, _ := handler.Handle(context.Background(), SkillFullTask, Message{
		Role:  "user",
		Parts: []Part{{Kind: "text", Text: "test"}},
	}, "")
	handler.mu.Lock()
	handler.tasks[task.ID].Status = TaskStatus{State: TaskStateWorking}
	handler.mu.Unlock()

	// RequestInput should trigger both callbacks.
	if err := handler.RequestInput(task.ID, "please provide more info"); err != nil {
		t.Fatalf("RequestInput failed: %v", err)
	}

	// Give async callbacks time to fire.
	time.Sleep(100 * time.Millisecond)

	taskEventMu.Lock()
	if !taskEventCalled {
		t.Error("onTaskEvent was not called for input-required transition")
	}
	taskEventMu.Unlock()

	pushMu.Lock()
	if !pushCalled {
		t.Error("pushNotifier was not called for input-required transition")
	}
	pushMu.Unlock()
}

// TestIssue1050_InputRequiredEventInUpdateStatus verifies that the
// updateStatus method handles TaskStateInputRequired and fires onTaskEvent.
func TestIssue1050_InputRequiredEventInUpdateStatus(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)

	var eventCalled bool
	var eventMu sync.Mutex

	handler.SetOnTaskEvent(func(msg TaskEventMessage) {
		eventMu.Lock()
		defer eventMu.Unlock()
		eventCalled = true
		if msg.Type != "input-required" {
			t.Errorf("expected event type 'input-required', got %s", msg.Type)
		}
	})

	// Create a task and immediately request input via RequestInput.
	// This internally calls updateStatus and should trigger onTaskEvent.
	task, _ := handler.Handle(context.Background(), SkillFullTask, Message{
		Role:  "user",
		Parts: []Part{{Kind: "text", Text: "test"}},
	}, "")
	handler.mu.Lock()
	handler.tasks[task.ID].Status = TaskStatus{State: TaskStateWorking}
	handler.mu.Unlock()

	if err := handler.RequestInput(task.ID, "please provide more info"); err != nil {
		t.Fatalf("RequestInput failed: %v", err)
	}

	// Give async callback time to fire.
	time.Sleep(100 * time.Millisecond)

	eventMu.Lock()
	if !eventCalled {
		t.Error("onTaskEvent was not called for input-required via RequestInput")
	}
	eventMu.Unlock()
}
