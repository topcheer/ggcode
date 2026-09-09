package tool

import (
	"strings"
	"testing"
)

func TestTodoWriteDescriptionEncouragesMeaningfulMilestones(t *testing.T) {
	tool := NewTodoWrite("test-desc-session")
	desc := tool.Description()
	for _, want := range []string{"genuinely multi-step work", "genuinely multi-step", "every milestone"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("todo_write description should mention %q, got %q", want, desc)
		}
	}
	params := string(tool.Parameters())
	for _, want := range []string{"Existing todos not in this list are removed", "include the full desired current list"} {
		if !strings.Contains(params, want) {
			t.Fatalf("todo_write schema should mention %q, got %s", want, params)
		}
	}
}

// TestTodoWriteCloneIsolation pins #1707 case 1: cloning (what Registry.Clone
// does per teammate) must yield an INDEPENDENT instance - mutating the
// clone's session must not flip the original's todo file.
func TestTodoWriteCloneIsolation(t *testing.T) {
	orig := NewTodoWrite("leader-session")
	clone, ok := orig.Clone().(*TodoWrite)
	if !ok {
		t.Fatal("Clone must return *TodoWrite")
	}
	clone.SetSessionID("tm-1")
	if orig.currentPath() != TodoFilePath("leader-session") {
		t.Fatalf("original must still point at the leader file, got %q", orig.currentPath())
	}
}
