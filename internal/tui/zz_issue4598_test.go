package tui

import (
	"os"
	"strings"
	"testing"
)

// #4598 legacy guard, updated for the #5842 semantics: the original fix
// protected mid-run pasted images from being DROPPED by a replace-form
// write (m.pendingImages = imgs). #5842 moved the drained images onto the
// interjection content blocks themselves (they travel WITH the interrupt -
// parking them back made a text-less image interjection a complete no-op),
// which preserves the #4598 intent (nothing is dropped) by a stronger
// mechanism. This test keeps guarding against the old replace-form write
// and pins the new travel-with-interrupt conversion.
func TestIssue4598_DrainMergesNotReplaces(t *testing.T) {
	b, err := os.ReadFile("model_pending.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	idx := strings.Index(s, "func (m *Model) drainPendingInterrupt")
	if idx < 0 {
		t.Fatal("drainPendingInterrupt not found")
	}
	end := strings.Index(s[idx+1:], "\nfunc ")
	if end < 0 {
		end = len(s) - idx - 1
	}
	body := s[idx : idx+1+end]
	if strings.Contains(body, "m.pendingImages = imgs\n") {
		t.Fatal("#4598 regression: replace-form write is back")
	}
	if !strings.Contains(body, "provider.ImageBlock(") {
		t.Fatal("#5842 regression: drained images no longer travel with the interjection as image blocks")
	}
}
