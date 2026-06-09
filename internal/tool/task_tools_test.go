package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

func TestTaskToolDescriptionsClarifyScope(t *testing.T) {
	createTool := TaskCreateTool{}
	for _, want := range []string{"todo_write", "swarm_task_create", "meaningful multi-step work"} {
		if !containsAny(createTool.Description(), want) {
			t.Fatalf("task_create description should mention %q, got %q", want, createTool.Description())
		}
	}

	stopTool := TaskStopTool{}
	for _, want := range []string{"task-board state only", "does not cancel"} {
		if !containsAny(stopTool.Description(), want) {
			t.Fatalf("task_stop description should mention %q, got %q", want, stopTool.Description())
		}
	}

	outputTool := TaskOutputTool{}
	for _, want := range []string{"not structured session task IDs", "read_command_output or wait_command"} {
		if !containsAny(outputTool.Description(), want) {
			t.Fatalf("task_output description should mention %q, got %q", want, outputTool.Description())
		}
	}
}

func TestTaskCreate_Basic(t *testing.T) {
	mgr := task.NewManager()
	tk := TaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"subject":     "Test task",
		"description": "A test task description",
	})
	result, err := tk.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "task-1") {
		t.Errorf("expected task ID, got: %s", result.Content)
	}
}

func TestTaskCreate_MissingSubject(t *testing.T) {
	mgr := task.NewManager()
	tk := TaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"description": "no subject",
	})
	result, err := tk.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing subject")
	}
}

func TestTaskList_Basic(t *testing.T) {
	mgr := task.NewManager()
	tk := TaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"subject":     "List test",
		"description": "Testing list",
	})
	tk.Execute(context.Background(), input)

	tl := TaskListTool{Manager: mgr}
	result, err := tl.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestTaskUpdate_Status(t *testing.T) {
	mgr := task.NewManager()
	tk := TaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"subject":     "Update test",
		"description": "Testing update",
	})
	tk.Execute(context.Background(), input)

	tu := TaskUpdateTool{Manager: mgr}
	input, _ = json.Marshal(map[string]interface{}{
		"taskId": "task-1",
		"status": "in_progress",
	})
	result, err := tu.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestTaskUpdate_InvalidStatus(t *testing.T) {
	mgr := task.NewManager()
	tk := TaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"subject": "Status test",
	})
	tk.Execute(context.Background(), input)

	tu := TaskUpdateTool{Manager: mgr}
	input, _ = json.Marshal(map[string]interface{}{
		"taskId": "task-1",
		"status": "invalid_status",
	})
	result, err := tu.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid status")
	}
}

func TestTaskGet_NotFound(t *testing.T) {
	mgr := task.NewManager()
	tg := TaskGetTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"taskId": "task-9999",
	})
	result, err := tg.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent task")
	}
}

func TestTaskStop_NotFound(t *testing.T) {
	mgr := task.NewManager()
	ts := TaskStopTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"taskId": "task-9999",
	})
	result, err := ts.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent task")
	}
}

func TestTaskOutput_NotFound(t *testing.T) {
	to := TaskOutputTool{Provider: noopTaskProvider{}}
	input, _ := json.Marshal(map[string]interface{}{
		"taskId": "nonexistent",
	})
	result, err := to.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent task")
	}
}

func TestTaskUpdate_StatusDoesNotRequireDescription(t *testing.T) {
	mgr := task.NewManager()
	tk := TaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"subject":     "Schema update test",
		"description": "Original description",
	})
	if result, err := tk.Execute(context.Background(), input); err != nil || result.IsError {
		t.Fatalf("create failed: result=%+v err=%v", result, err)
	}

	tu := TaskUpdateTool{Manager: mgr}
	input, _ = json.Marshal(map[string]interface{}{
		"taskId": "task-1",
		"status": "completed",
	})
	result, err := tu.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error updating status without description: %s", result.Content)
	}

	got, ok := mgr.Get("task-1")
	if !ok {
		t.Fatal("expected task to exist")
	}
	if got.Description != "Original description" {
		t.Fatalf("status-only update should preserve description, got %q", got.Description)
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("expected completed status, got %s", got.Status)
	}

	params := string(tu.Parameters())
	if strings.Contains(params, `"taskId",
			"description"`) {
		t.Fatal("task_update schema should not require description for status-only updates")
	}
}

type noopTaskProvider struct{}

func (noopTaskProvider) GetTaskOutput(taskID string) (string, bool) {
	return "", false
}
