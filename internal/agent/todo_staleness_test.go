package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTodoStaleness_Reset(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(5, 3)
	s.markReminded(10)

	s.reset()

	if s.lastUpdateIter != -1 || s.hasTodos || s.remindedCount != 0 || s.reminderActive || s.lastRemindedIter != -1 {
		t.Error("state not fully reset")
	}
}

func TestTodoStaleness_RecordUpdate(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(3, 5)

	if s.lastUpdateIter != 3 || !s.hasTodos {
		t.Errorf("expected lastUpdateIter=3, hasTodos=true; got iter=%d, has=%v", s.lastUpdateIter, s.hasTodos)
	}

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
	s.recordUpdate(2, 5)

	if s.shouldRemind(2+staleTodoThreshold-1, true) {
		t.Error("should not remind below threshold")
	}

	if !s.shouldRemind(2+staleTodoThreshold, true) {
		t.Error("should remind at threshold with incomplete todos")
	}

	s.markReminded(2 + staleTodoThreshold)

	if s.shouldRemind(2+staleTodoThreshold+1, true) {
		t.Error("should not remind again after markReminded (within grace)")
	}

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

	if s.shouldRemind(2+staleTodoThreshold, false) {
		t.Error("should not remind when all todos are done")
	}
}

func TestTodoStaleness_NoRemindWhenNoTodos(t *testing.T) {
	s := newTodoStalenessState()

	if s.shouldRemind(staleTodoThreshold+5, true) {
		t.Error("should not remind when no todos were ever created")
	}
}

func TestTodoStaleness_MaxReminders(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(1, 5)

	if !s.shouldRemind(1+staleTodoThreshold, true) {
		t.Fatal("should remind (1st)")
	}
	s.markReminded(1 + staleTodoThreshold)

	if s.shouldRemind(1+staleTodoThreshold+reminderGraceIters-1, true) {
		t.Error("should not remind within grace window")
	}

	if !s.shouldRemind(1+staleTodoThreshold+reminderGraceIters, true) {
		t.Fatal("should remind (2nd) after grace expiry")
	}
	s.markReminded(1 + staleTodoThreshold + reminderGraceIters)

	for n := 3; n <= maxStaleTodoReminders; n++ {
		at := 1 + staleTodoThreshold + (n-1)*reminderGraceIters
		if !s.shouldRemind(at, true) {
			t.Fatalf("should remind (%dth) after grace expiry", n)
		}
		s.markReminded(at)
	}

	if s.shouldRemind(1+staleTodoThreshold+(maxStaleTodoReminders+2)*reminderGraceIters, true) {
		t.Error("should not exceed maxStaleTodoReminders")
	}
	if maxStaleTodoReminders < 5 {
		t.Errorf("#4855: budget should be >= 5, got %d", maxStaleTodoReminders)
	}
}

func TestTodoStaleness_LongRunCoverage(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(2, 4)

	fired := 0
	for iter := 3; iter <= 3+40; iter++ {
		if s.shouldRemind(iter, true) {
			s.markReminded(iter)
			fired++
		}
	}
	if fired != maxStaleTodoReminders {
		t.Errorf("long bare run fired %d reminders, want %d (budget fully used)", fired, maxStaleTodoReminders)
	}
}

func TestStaleTodoReminderText(t *testing.T) {
	items := []string{"[in_progress] wire reminder text", "[pending] add grace expiry", "[pending] raise budget"}
	text := staleTodoReminderText(3, 5, 10, items)
	if text == "" {
		t.Fatal("reminder text should not be empty")
	}

	if !strings.Contains(text, "10 iterations") {
		t.Error("reminder should mention iteration count")
	}
	if !strings.Contains(text, "3 of 5") {
		t.Error("reminder should mention incomplete/total counts")
	}
	if !strings.Contains(text, "todo_write") {
		t.Error("reminder should mention todo_write tool")
	}

	for _, want := range items {
		if !strings.Contains(text, want) {
			t.Errorf("reminder should embed item %q", want)
		}
	}

	if staleTodoReminderText(2, 2, 9, nil) == "" {
		t.Error("reminder text should build with nil items")
	}
}

func TestParseTodoCount(t *testing.T) {

	input := json.RawMessage(`{"todos":[{"id":"1","content":"a","status":"done"},{"id":"2","content":"b","status":"in_progress"},{"id":"3","content":"c","status":"pending"}]}`)
	if count := parseTodoCount(input); count != 3 {
		t.Errorf("expected 3, got %d", count)
	}

	input = json.RawMessage(`{"todos":[]}`)
	if count := parseTodoCount(input); count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	input = json.RawMessage(`{bad json}`)
	if count := parseTodoCount(input); count != 0 {
		t.Errorf("expected 0 for invalid JSON, got %d", count)
	}

	input = json.RawMessage(`{"foo":"bar"}`)
	if count := parseTodoCount(input); count != 0 {
		t.Errorf("expected 0 for missing field, got %d", count)
	}
}

func TestTodoStaleness_BudgetResetsOnUpdate(t *testing.T) {
	s := newTodoStalenessState()
	s.recordUpdate(0, 3)

	for i := 1; i <= maxStaleTodoReminders; i++ {
		iter := 8 + (i-1)*5
		if !s.shouldRemind(iter, true) {
			t.Fatalf("reminder %d/%d should fire at iter %d", i, maxStaleTodoReminders, iter)
		}
		s.markReminded(iter)
	}
	exhausted := 8 + maxStaleTodoReminders*5
	if s.shouldRemind(exhausted, true) {
		t.Fatal("budget exhausted within one ignore-window: no more reminders expected")
	}

	s.recordUpdate(60, 3)
	if s.remindedCount != 0 {
		t.Fatalf("budget must reset on todo_write, got remindedCount=%d", s.remindedCount)
	}

	if !s.shouldRemind(60+staleTodoThreshold+1, true) {
		t.Fatal("post-update stagnation must trigger reminders again (long-run guard re-armed)")
	}
}
