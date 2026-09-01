package tui

import "testing"

// TestConsumeVisiblePrefixReturnsImages pins #1411-A: Esc-cancel restore
// used to drop the screenshots attached to queued messages - only the text
// came back, the placeholder bubble was removed, no hint remained.
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
