package context

import (
	"strings"
	"testing"
)

func TestPinnedContext_AddAndRender(t *testing.T) {
	p := newPinnedContext()

	// Empty initially
	if !p.IsEmpty() {
		t.Fatal("expected empty pinned context")
	}
	if p.Render() != "" {
		t.Fatal("expected empty render")
	}

	// Add an item
	id, err := p.Add("Build with: go build -tags goolm ./...")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	rendered := p.Render()
	if !strings.Contains(rendered, "[Pinned Context") {
		t.Fatalf("render should contain marker: %q", rendered)
	}
	if !strings.Contains(rendered, "go build -tags goolm") {
		t.Fatalf("render should contain pinned text: %q", rendered)
	}
	if !strings.Contains(rendered, "1.") {
		t.Fatalf("render should contain index: %q", rendered)
	}

	items := p.List()
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Text != "Build with: go build -tags goolm ./..." {
		t.Fatalf("unexpected text: %q", items[0].Text)
	}
}

func TestPinnedContext_AddEmpty(t *testing.T) {
	p := newPinnedContext()
	_, err := p.Add("   ")
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestPinnedContext_MaxItems(t *testing.T) {
	p := newPinnedContext()
	for i := 0; i < maxPinnedItems; i++ {
		_, err := p.Add("item")
		if err != nil {
			t.Fatalf("Add %d failed: %v", i, err)
		}
	}
	_, err := p.Add("overflow")
	if err == nil {
		t.Fatal("expected error when exceeding max items")
	}
}

func TestPinnedContext_RemoveByIndex(t *testing.T) {
	p := newPinnedContext()
	p.Add("first")
	p.Add("second")
	p.Add("third")

	if !p.Remove("2") {
		t.Fatal("expected Remove(2) to succeed")
	}
	items := p.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 items after remove, got %d", len(items))
	}
	if items[0].Text != "first" || items[1].Text != "third" {
		t.Fatalf("wrong items after remove: %+v", items)
	}
}

func TestPinnedContext_RemoveByIDPrefix(t *testing.T) {
	p := newPinnedContext()
	id, _ := p.Add("test item")

	// ID is like "pin_abcd1234", use prefix
	if !p.Remove(id[:6]) {
		t.Fatal("expected Remove by ID prefix to succeed")
	}
	if !p.IsEmpty() {
		t.Fatal("expected empty after removing by ID")
	}
}

func TestPinnedContext_RemoveNotFound(t *testing.T) {
	p := newPinnedContext()
	p.Add("item")
	if p.Remove("99") {
		t.Fatal("expected Remove(99) to fail (out of range)")
	}
	if p.Remove("nonexistent") {
		t.Fatal("expected Remove(nonexistent) to fail")
	}
}

func TestPinnedContext_Clear(t *testing.T) {
	p := newPinnedContext()
	p.Add("a")
	p.Add("b")

	n := p.Clear()
	if n != 2 {
		t.Fatalf("expected Clear to return 2, got %d", n)
	}
	if !p.IsEmpty() {
		t.Fatal("expected empty after Clear")
	}

	// Clear on empty returns 0
	n = p.Clear()
	if n != 0 {
		t.Fatalf("expected Clear on empty to return 0, got %d", n)
	}
}

func TestPinnedContext_TotalBudgetEnforcement(t *testing.T) {
	p := newPinnedContext()
	// Fill up close to the total budget
	chunk := strings.Repeat("x", 1000)
	for i := 0; i < maxPinnedTotal/1000; i++ {
		_, err := p.Add(chunk)
		if err != nil {
			t.Fatalf("Add %d failed: %v", i, err)
		}
	}
	// Adding one more should fail (budget exceeded)
	_, err := p.Add(chunk)
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
}

func TestPinnedContext_ItemTruncation(t *testing.T) {
	p := newPinnedContext()
	longText := strings.Repeat("a", maxPinnedChars+500)
	id, err := p.Add(longText)
	if err != nil {
		t.Fatalf("Add of long text failed: %v", err)
	}
	items := p.List()
	found := false
	for _, item := range items {
		if item.ID == id {
			if len(item.Text) > maxPinnedChars {
				t.Fatalf("item not truncated: %d chars", len(item.Text))
			}
			found = true
		}
	}
	if !found {
		t.Fatal("added item not found")
	}
}
