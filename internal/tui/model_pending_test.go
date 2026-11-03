package tui

import "testing"

func TestConsumeVisiblePrefixReturnsImages(t *testing.T) {
	q := &pendingQueue{}
	imgs := []imageAttachedMsg{{sourcePath: "shot.png"}}
	q.enqueueWithImages("check this", imgs)
	q.enqueueWithImages("second queued", nil)

	text, got := q.consumeVisiblePrefix()
	if text != "check this\n\nsecond queued" {
		t.Fatalf("text = %q", text)
	}
	if len(got) != 1 || got[0].sourcePath != "shot.png" {
		t.Fatalf("images not restored: %#v", got)
	}
	if text, got = q.consumeVisiblePrefix(); text != "" || len(got) != 0 {
		t.Fatalf("second consume should be empty: %q %#v", text, got)
	}
}

func TestResetConversationViewClearsTodos(t *testing.T) {
	m := newTestModel()
	m.todoSnapshot = map[string]todoStateItem{"a1": {ID: "a1", Status: "in_progress"}}
	m.todoOrder = []string{"a1"}
	m.activeTodo = &todoStateItem{ID: "a1"}

	m.resetConversationView()

	if m.todoSnapshot != nil || m.todoOrder != nil || m.activeTodo != nil {
		t.Fatalf("todo state leaked across sessions: snapshot=%v order=%v active=%v", m.todoSnapshot, m.todoOrder, m.activeTodo)
	}
}
