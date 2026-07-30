package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTodoStaleness_Reset(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(5, 3)
	s.markReminded()

	s.reset()

	if s.lastUpdateIter != -1 || s.hasTodos || s.remindedCount != 0 || s.reminderActive {
		t.Error("state not fully reset")
	}
}

func TestTodoStaleness_RecordUpdate(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(3, 5)

	if s.lastUpdateIter != 3 || !s.hasTodos {
		t.Errorf("expected lastUpdateIter=3, hasTodos=true; got iter=%d, has=%v", s.lastUpdateIter, s.hasTodos)
	}

	// recordUpdate should clear reminderActive
	s.reminderActive = true
	s.recordUpdate(10, 2)
	if s.reminderActive {
		t.Error("recordUpdate should clear reminderActive")
	}
	if s.lastUpdateIter != 10 {
		t.Errorf("expected lastUpdateIter=10, got %d", s.lastUpdateIter)
	}
}

func TestTodoStaleness_ShouldRemind(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(2, 5) // todos created at iteration 2

	// Below threshold: no reminder
	if s.shouldRemind(2+staleTodoThreshold-1, true) {
		t.Error("should not remind below threshold")
	}

	// At threshold, with incomplete todos: should remind
	if !s.shouldRemind(2+staleTodoThreshold, true) {
		t.Error("should remind at threshold with incomplete todos")
	}

	s.markReminded()

	// After marking reminded, should not remind again
	if s.shouldRemind(2+staleTodoThreshold+1, true) {
		t.Error("should not remind again after markReminded")
	}

	// After a new update, reminderActive clears, and staleness can fire again
	s.recordUpdate(20, 5)
	if s.shouldRemind(20+staleTodoThreshold-1, true) {
		t.Error("should not remind below threshold after new update")
	}
	if !s.shouldRemind(20+staleTodoThreshold, true) {
		t.Error("should remind after new stagnation period")
	}
}

func TestTodoStaleness_NoRemindWhenAllDone(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(2, 5)

	// All done: no reminder even at threshold
	if s.shouldRemind(2+staleTodoThreshold, false) {
		t.Error("should not remind when all todos are done")
	}
}

func TestTodoStaleness_NoRemindWhenNoTodos(t *testing.T) {
	s := newTodoStalenessState()
	// Never recorded any update
	if s.shouldRemind(staleTodoThreshold+5, true) {
		t.Error("should not remind when no todos were ever created")
	}
}

func TestTodoStaleness_MaxReminders(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(1, 5)

	// Fire first reminder
	if !s.shouldRemind(1+staleTodoThreshold, true) {
		t.Fatal("should remind (1st)")
	}
	s.markReminded()

	// Agent doesn't update — but reminderActive prevents re-firing
	// Simulate clearing reminderActive (which would happen if agent updated)
	s.reminderActive = false

	// Second reminder allowed
	if !s.shouldRemind(1+staleTodoThreshold+5, true) {
		t.Fatal("should remind (2nd)")
	}
	s.markReminded()

	// Now remindedCount = 2 = max, no more reminders even after clearing active
	s.reminderActive = false
	if s.shouldRemind(1+staleTodoThreshold+10, true) {
		t.Error("should not exceed maxStaleTodoReminders")
	}
}

func TestStaleTodoReminderText(t *testing.T) {
	text := staleTodoReminderText(3, 5, 10)
	if text == "" {
		t.Fatal("reminder text should not be empty")
	}
	// Should contain the key info
	if !strings.Contains(text, "10 iterations") {
		t.Error("reminder should mention iteration count")
	}
	if !strings.Contains(text, "3 of 5") {
		t.Error("reminder should mention incomplete/total counts")
	}
	if !strings.Contains(text, "todo_write") {
		t.Error("reminder should mention todo_write tool")
	}
}

func TestParseTodoCount(t *testing.T) {
	// Valid input with 3 todos
	input := json.RawMessage(`{"todos":[{"id":"1","content":"a","status":"done"},{"id":"2","content":"b","status":"in_progress"},{"id":"3","content":"c","status":"pending"}]}`)
	if count := parseTodoCount(input); count != 3 {
		t.Errorf("expected 3, got %d", count)
	}

	// Empty array
	input = json.RawMessage(`{"todos":[]}`)
	if count := parseTodoCount(input); count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	// Invalid JSON
	input = json.RawMessage(`{bad json}`)
	if count := parseTodoCount(input); count != 0 {
		t.Errorf("expected 0 for invalid JSON, got %d", count)
	}

	// Missing todos field
	input = json.RawMessage(`{"foo":"bar"}`)
	if count := parseTodoCount(input); count != 0 {
		t.Errorf("expected 0 for missing field, got %d", count)
	}
}
