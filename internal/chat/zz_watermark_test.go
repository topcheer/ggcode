package chat

import (
	"os"
	"testing"
)

// Watermark default + env escape hatch (#watermark-250).
func TestDefaultMaxItems250(t *testing.T) {
	os.Unsetenv("GGCODE_CHAT_MAX_ITEMS")
	if DefaultMaxItems != 250 {
		t.Fatalf("DefaultMaxItems = %d, want 250", DefaultMaxItems)
	}
	if got := defaultMaxItems(); got != 250 {
		t.Fatalf("defaultMaxItems() = %d, want 250", got)
	}
}

func TestMaxItemsEnvOverride(t *testing.T) {
	t.Setenv("GGCODE_CHAT_MAX_ITEMS", "5000")
	if got := defaultMaxItems(); got != 5000 {
		t.Fatalf("env override = %d, want 5000", got)
	}
	t.Setenv("GGCODE_CHAT_MAX_ITEMS", "0")
	if got := defaultMaxItems(); got != 250 {
		t.Fatalf("env=0 must fall back to default, got %d", got)
	}
	t.Setenv("GGCODE_CHAT_MAX_ITEMS", "garbage")
	if got := defaultMaxItems(); got != 250 {
		t.Fatalf("invalid env must fall back to default, got %d", got)
	}
}

// Trim keeps the tail: at the 250 watermark the newest items (including an
// in-flight streaming item appended last) survive and old ones drop.
func TestTrimAtWatermarkKeepsTail(t *testing.T) {
	os.Unsetenv("GGCODE_CHAT_MAX_ITEMS")
	l := NewList(80, 20)
	l.SetMaxItems(10) // small watermark to exercise the same path
	for i := 0; i < 20; i++ {
		l.Append(NewSystemItem(string(rune('a'+i)), "x", Styles{}))
	}
	l.mu.RLock()
	n := len(l.items)
	first := l.items[0].ID()
	last := l.items[n-1].ID()
	l.mu.RUnlock()
	if n != 8 { // steady state: 11 -> trim to 7, +4 appends -> 11 -> trim to 7, +1 = 8
		t.Fatalf("len = %d, want 8", n)
	}
	if first != "m" || last != "t" { // oldest kept = index 12 after final trim
		t.Fatalf("tail not kept: first=%s last=%s", first, last)
	}
}
