package agent

import (
	"encoding/json"
	"testing"
)

func todoArgs(t *testing.T, items ...todoItem) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{
		"todos": items,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTodoDropDetectsSilentRemoval(t *testing.T) {
	d := newTodoDropState()
	// First call: establish a list with two pending items.
	d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Implement function A", Status: "in_progress"},
		todoItem{ID: "2", Content: "Write tests for A", Status: "pending"},
	))
	// Second call: only item 1 remains (marked done), item 2 silently dropped.
	msg := d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Implement function A", Status: "done"},
	))
	if msg == "" {
		t.Fatal("expected drop detection for silently removed item 2")
	}
	if !todoDropContains(msg, "Write tests for A") {
		t.Errorf("guidance should mention dropped item content, got: %s", msg)
	}
}

func TestTodoDropNoFalsePositiveOnDoneItems(t *testing.T) {
	d := newTodoDropState()
	d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Task A", Status: "pending"},
		todoItem{ID: "2", Content: "Task B", Status: "pending"},
	))
	// Both items completed then removed: no drop.
	d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Task A", Status: "done"},
		todoItem{ID: "2", Content: "Task B", Status: "done"},
	))
	// Third call with only item 1.
	msg := d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Task A", Status: "done"},
	))
	if msg != "" {
		t.Errorf("should not flag removal of completed items, got: %s", msg)
	}
}

func TestTodoDropRewordedItemNotFlagged(t *testing.T) {
	d := newTodoDropState()
	d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Implement the authentication module with JWT tokens", Status: "pending"},
	))
	// Same item, reworded but highly similar.
	msg := d.checkDrop(todoArgs(t,
		todoItem{ID: "1b", Content: "Implement the authentication module with JWT tokens and tests", Status: "in_progress"},
	))
	if msg != "" {
		t.Errorf("reworded item should not be flagged as drop, got: %s", msg)
	}
}

func TestTodoDropMultipleDroppedItems(t *testing.T) {
	d := newTodoDropState()
	d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Task one", Status: "pending"},
		todoItem{ID: "2", Content: "Task two", Status: "pending"},
		todoItem{ID: "3", Content: "Task three", Status: "pending"},
	))
	msg := d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Task one", Status: "done"},
	))
	if msg == "" {
		t.Fatal("expected detection for 2 dropped items")
	}
	if !todoDropContains(msg, "2 ") && !todoDropContains(msg, "2\n") {
		t.Errorf("should mention 2 dropped items, got: %s", msg)
	}
}

func TestTodoDropFiresOnlyOnce(t *testing.T) {
	d := newTodoDropState()
	d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Task one", Status: "pending"},
		todoItem{ID: "2", Content: "Task two", Status: "pending"},
	))
	msg1 := d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Task one", Status: "done"},
	))
	if msg1 == "" {
		t.Fatal("first drop should fire")
	}
	// Third call: another drop but should not fire again.
	msg2 := d.checkDrop(todoArgs(t))
	if msg2 != "" {
		t.Errorf("should only fire once per run, got second message: %s", msg2)
	}
}

func TestTodoDropEmptyListAfterNonEmpty(t *testing.T) {
	d := newTodoDropState()
	d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Important task", Status: "in_progress"},
	))
	msg := d.checkDrop(todoArgs(t))
	if msg == "" {
		t.Fatal("dropping all items to empty list should fire")
	}
	if !todoDropContains(msg, "Important task") {
		t.Errorf("should mention dropped item, got: %s", msg)
	}
}

func TestTodoDropReset(t *testing.T) {
	d := newTodoDropState()
	d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Task one", Status: "pending"},
	))
	d.checkDrop(todoArgs(t))
	d.reset()
	// After reset, should be able to fire again.
	msg := d.checkDrop(todoArgs(t,
		todoItem{ID: "1", Content: "Task one", Status: "pending"},
	))
	if msg != "" {
		t.Errorf("after reset, first call should not fire (no previous list)")
	}
}

func TestJaccardSimilarity(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"hello world", "hello world", 1.0},
		{"hello world", "goodbye world", 1.0 / 3.0},
		{"", "test", 0},
		{"test", "", 0},
		{"a b c", "a b c d", 3.0 / 4.0},
	}
	for _, tt := range tests {
		got := todoDropJaccard(tt.a, tt.b)
		if abs(got-tt.want) > 0.01 {
			t.Errorf("todoDropJaccard(%q, %q) = %.3f, want %.3f", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestTodoDropTruncateStr(t *testing.T) {
	if got := truncateStr("short", 10); got != "short" {
		t.Errorf("truncateStr short = %q", got)
	}
	if got := truncateStr("this is a very long string that needs truncation", 20); len(got) != 20 {
		t.Errorf("truncateStr result length = %d, want 20", len(got))
	}
}

func TestParseTodoItems(t *testing.T) {
	input := todoArgs(t,
		todoItem{ID: "1", Content: "Task 1", Status: "done"},
		todoItem{ID: "2", Content: "Task 2", Status: "pending"},
	)
	items := parseTodoItems(input)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ID != "1" || items[1].Status != "pending" {
		t.Errorf("unexpected items: %+v", items)
	}
}

func TestParseTodoItemsInvalidJSON(t *testing.T) {
	items := parseTodoItems(json.RawMessage(`{invalid}`))
	if items != nil {
		t.Errorf("expected nil for invalid JSON, got %v", items)
	}
}

func todoDropContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && todoDropContainsStr(s, substr))
}

func todoDropContainsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
