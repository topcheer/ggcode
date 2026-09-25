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
	for _, want := range []string{"read_command_output/wait_command"} {
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

// r78 plan-graph integrity: task_list must reflect blocker completion state,
// not render "(blocked by ...)" forever after every blocker finished.
func TestTaskList_BlockerCompletionState(t *testing.T) {
	mgr := task.NewManager()
	create := TaskCreateTool{Manager: mgr}
	update := TaskUpdateTool{Manager: mgr}
	mk := func(subject string) string {
		input, _ := json.Marshal(map[string]interface{}{
			"subject": subject, "description": "d",
		})
		res, err := create.Execute(context.Background(), input)
		if err != nil || res.IsError {
			t.Fatalf("create %s failed: %v %s", subject, err, res.Content)
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(res.Content), &created); err != nil {
			t.Fatal(err)
		}
		return created.ID
	}

	blockerA := mk("blocker A")
	blockerB := mk("blocker B")
	mk("blocked leaf")
	// leaf blocked by A and B
	leaf := mgr.List()
	var leafID string
	for _, tk := range leaf {
		if tk.Subject == "blocked leaf" {
			leafID = tk.ID
		}
	}
	upd, _ := json.Marshal(map[string]interface{}{"taskId": leafID, "addBlockedBy": []string{blockerA, blockerB}})
	if res, err := update.Execute(context.Background(), upd); err != nil || res.IsError {
		t.Fatalf("link deps failed: %v %s", err, res.Content)
	}

	tl := TaskListTool{Manager: mgr}
	out := func() string {
		res, err := tl.Execute(context.Background(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return res.Content
	}

	// Both blockers open: plain blocked rendering, no ready/done noise.
	got := out()
	if !strings.Contains(got, "(blocked by "+blockerA+", "+blockerB+")") {
		t.Errorf("expected plain blocked rendering, got: %s", got)
	}

	// Complete A: leaf shows remaining open blocker + done annotation.
	completed := task.StatusCompleted
	if _, err := mgr.Update(blockerA, task.UpdateOptions{Status: &completed}); err != nil {
		t.Fatal(err)
	}
	got = out()
	if !strings.Contains(got, "(blocked by "+blockerB+"; "+blockerA+" already done)") {
		t.Errorf("expected mixed blocker rendering, got: %s", got)
	}

	// Complete B: leaf becomes ready, no stale "(blocked by ...)" remains.
	if _, err := mgr.Update(blockerB, task.UpdateOptions{Status: &completed}); err != nil {
		t.Fatal(err)
	}
	got = out()
	if strings.Contains(got, "blocked by") {
		t.Errorf("completed blockers must not render as blocked, got: %s", got)
	}
	if !strings.Contains(got, "(ready: blockers "+blockerA+", "+blockerB+" completed)") {
		t.Errorf("expected ready annotation, got: %s", got)
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

// #1346: the subject gate must count runes, not bytes - 150 CJK chars
// (450 bytes) is within the documented 200-char limit.
func TestTaskCreate_CJKSubjectRuneGate(t *testing.T) {
	mgr := task.NewManager()
	tool := TaskCreateTool{Manager: mgr}

	subject := strings.Repeat("任", 150) // 450 bytes, 150 runes
	input, _ := json.Marshal(map[string]interface{}{"subject": subject})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("150-rune CJK subject must be accepted (max 200 chars), got: %s", res.Content)
	}

	// 250 runes exceeds the limit and must still be rejected.
	tooLong := strings.Repeat("任", 250)
	input, _ = json.Marshal(map[string]interface{}{"subject": tooLong})
	res, err = tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("250-rune subject must be rejected")
	}
}
