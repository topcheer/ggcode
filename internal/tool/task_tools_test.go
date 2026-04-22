package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

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

type noopTaskProvider struct{}

func (noopTaskProvider) GetTaskOutput(taskID string) (string, bool) {
	return "", false
}
