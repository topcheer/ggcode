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
