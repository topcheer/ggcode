package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestCheckIncompleteTodos_NoTodos(t *testing.T) {
	withTestHomeTodos(t)
	a := &Agent{}
	reg := tool.NewRegistry()
	tw := tool.NewTodoWrite("test-session-none")
	reg.Register(tw)
	a.tools = reg

	if msg := a.checkIncompleteTodos(); msg != "" {
		t.Errorf("expected empty message with no todos, got: %s", msg)
	}

	tw.ClearTodos()
}

func TestCheckIncompleteTodos_AllDone(t *testing.T) {
	withTestHomeTodos(t)
	a := &Agent{}
	reg := tool.NewRegistry()
	tw := tool.NewTodoWrite("test-session-all-done")
	reg.Register(tw)
	a.tools = reg

	input, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "task A", "status": "done"},
			{"id": "2", "content": "task B", "status": "done"},
		},
	})
	_, err := tw.Execute(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}

	if msg := a.checkIncompleteTodos(); msg != "" {
		t.Errorf("expected empty message when all done, got: %s", msg)
	}

	tw.ClearTodos()
}

func TestCheckIncompleteTodos_HasPending(t *testing.T) {
	withTestHomeTodos(t)
	a := &Agent{}
	reg := tool.NewRegistry()
	tw := tool.NewTodoWrite("test-session-pending")
	reg.Register(tw)
	a.tools = reg

	input, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "completed task", "status": "done"},
			{"id": "2", "content": "pending task", "status": "pending"},
			{"id": "3", "content": "in progress task", "status": "in_progress"},
		},
	})
	_, err := tw.Execute(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}

	msg := a.checkIncompleteTodos()
	if msg == "" {
		t.Fatal("expected non-empty reminder for incomplete todos")
	}
	if !strings.Contains(msg, "pending task") {
		t.Errorf("expected reminder to mention 'pending task', got: %s", msg)
	}
	if !strings.Contains(msg, "in progress task") {
		t.Errorf("expected reminder to mention 'in progress task', got: %s", msg)
	}
	if !strings.Contains(msg, "2 of 3") {
		t.Errorf("expected '2 of 3' in reminder, got: %s", msg)
	}

	tw.ClearTodos()
}

func TestCheckIncompleteTodos_AllPending(t *testing.T) {
	withTestHomeTodos(t)
	a := &Agent{}
	reg := tool.NewRegistry()
	tw := tool.NewTodoWrite("test-session-all-pending")
	reg.Register(tw)
	a.tools = reg

	input, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "task A", "status": "pending"},
			{"id": "2", "content": "task B", "status": "in_progress"},
		},
	})
	_, err := tw.Execute(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}

	msg := a.checkIncompleteTodos()
	if msg == "" {
		t.Fatal("expected non-empty reminder")
	}
	if !strings.Contains(msg, "None of your todos") {
		t.Errorf("expected 'None of your todos' message when all incomplete, got: %s", msg)
	}

	tw.ClearTodos()
}

func TestCodeChangedInRun_NoEdits(t *testing.T) {
	rs := &RunStats{ToolCalls: map[string]int{
		"read_file":   3,
		"grep":        2,
		"run_command": 1,
	}}
	if codeChangedInRun(rs) {
		t.Error("expected false when no editing tools used")
	}
}

func TestCodeChangedInRun_WithEdits(t *testing.T) {
	rs := &RunStats{ToolCalls: map[string]int{
		"read_file":  3,
		"edit_file":  1,
		"write_file": 2,
	}}
	if !codeChangedInRun(rs) {
		t.Error("expected true when editing tools used")
	}
}

func TestCodeChangedInRun_WithMultiEdit(t *testing.T) {
	rs := &RunStats{ToolCalls: map[string]int{
		"multi_edit_file": 1,
	}}
	if !codeChangedInRun(rs) {
		t.Error("expected true when multi_edit_file used")
	}
}

func TestCodeChangedInRun_EmptyStats(t *testing.T) {
	rs := &RunStats{}
	if codeChangedInRun(rs) {
		t.Error("expected false for empty stats")
	}
}

func TestCodeChangedInRun_NilMap(t *testing.T) {
	rs := &RunStats{ToolCalls: nil}
	if codeChangedInRun(rs) {
		t.Error("expected false for nil map")
	}
}

// Helper to isolate HOME for todo tests.
func withTestHomeTodos(t *testing.T) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
}
