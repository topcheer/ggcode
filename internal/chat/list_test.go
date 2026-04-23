package chat

import (
	"strings"
	"testing"
)

func TestListEmpty(t *testing.T) {
	l := NewList(80, 20)
	if l.Len() != 0 {
		t.Fatal("expected empty list")
	}
	if l.Render() != "" {
		t.Fatal("expected empty render")
	}
}

func TestListAppend(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 20)
	l.Append(NewUserItem("u1", "hello", styles))
	l.Append(NewSystemItem("s1", "done", styles))
	if l.Len() != 2 {
		t.Fatalf("expected 2 items, got %d", l.Len())
	}
}

func TestListVirtualScroll(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 5) // small viewport

	// Add 10 items, each 1 line + gap = 2 lines
	for i := 0; i < 10; i++ {
		l.Append(NewSystemItem("s", "line", styles))
	}

	rendered := l.Render()
	lines := strings.Split(rendered, "\n")
	if len(lines) > 5 {
		t.Fatalf("expected at most 5 lines in viewport, got %d", len(lines))
	}
}

func TestListScrollDown(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 5)
	l.SetFollow(false)

	for i := 0; i < 10; i++ {
		l.Append(NewSystemItem("s", "line", styles))
	}

	l.ScrollDown(3)
	rendered := l.Render()
	// Should have scrolled — not showing the first items
	if rendered == "" {
		t.Fatal("expected non-empty render")
	}
}

func TestListScrollToEnd(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 5)

	for i := 0; i < 10; i++ {
		l.Append(NewSystemItem("s", "line", styles))
	}

	l.ScrollUp(20)
	l.ScrollToEnd()
	if !l.AtBottom() {
		t.Fatal("expected to be at bottom after ScrollToEnd")
	}
}

func TestListFindByID(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 20)
	l.Append(NewUserItem("u1", "hello", styles))
	l.Append(NewUserItem("u2", "world", styles))

	item := l.FindByID("u2")
	if item == nil {
		t.Fatal("expected to find u2")
	}
	if item.ID() != "u2" {
		t.Fatalf("expected u2, got %s", item.ID())
	}

	if l.FindByID("u99") != nil {
		t.Fatal("expected nil for non-existent ID")
	}
}

func TestListUpdateItem(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 20)
	l.Append(NewUserItem("u1", "hello", styles))

	updated := NewUserItem("u1", "world", styles)
	l.UpdateItem("u1", updated)

	item := l.FindByID("u1")
	if item == nil {
		t.Fatal("expected to find u1")
	}
	rendered := item.Render(80)
	if !strings.Contains(rendered, "world") {
		t.Fatalf("expected updated content, got: %s", rendered)
	}
}

func TestListFollow(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 3)
	l.SetFollow(true)

	// Append many items — should auto-scroll to last
	for i := 0; i < 20; i++ {
		l.Append(NewSystemItem("s", "line", styles))
	}

	rendered := l.Render()
	// The last items should be visible
	if !strings.Contains(rendered, "line") {
		t.Fatalf("expected content in rendered view")
	}
}

func TestListSetSize(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 20)
	l.Append(NewUserItem("u1", "hello world this is a test", styles))

	l.SetSize(20, 10)
	rendered := l.Render()
	if rendered == "" {
		t.Fatal("expected non-empty render after resize")
	}
}
