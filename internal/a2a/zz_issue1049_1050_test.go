//go:build integration_local

package a2a

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestIssue1049_CancelTaskPushAsync(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0, APIKey: "test-key"}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	task, _ := handler.Handle(context.Background(), SkillFullTask, Message{
		Role:  "user",
		Parts: []Part{{Kind: "text", Text: "test"}},
	}, "")
	handler.mu.Lock()
	handler.tasks[task.ID].Status = TaskStatus{State: TaskStateWorking}
	handler.mu.Unlock()

	var pushCalled bool
	var pushMu sync.Mutex
	handler.SetPushNotifier(func(taskID string, payload StreamResponse) {
		pushMu.Lock()
		defer pushMu.Unlock()
		pushCalled = true
		time.Sleep(100 * time.Millisecond)
	})

	start := time.Now()
	if err := handler.CancelTask(task.ID); err != nil {
		t.Fatalf("CancelTask failed: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("CancelTask took too long (%v), should return before push callback completes", elapsed)
	}

	time.Sleep(200 * time.Millisecond)
	pushMu.Lock()
	if !pushCalled {
		t.Error("push callback was never called")
	}
	pushMu.Unlock()
}

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

	time.Sleep(100 * time.Millisecond)

	eventMu.Lock()
	if !eventCalled {
		t.Error("onTaskEvent was not called for input-required via RequestInput")
	}
	eventMu.Unlock()
}
