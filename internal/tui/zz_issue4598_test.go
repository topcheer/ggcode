//go:build goolm

package tui

import (
	"os"
	"strings"
	"testing"
)

// #4598 pin: drainPendingInterrupt runs on the AGENT goroutine and must
// MERGE the drained images back into pendingImages, not replace the slice -
// the old `m.pendingImages = imgs` silently dropped images pasted mid-run
// by the Update goroutine.
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
	if !strings.Contains(body, "m.pendingImages = append(m.pendingImages, imgs...)") {
		t.Fatal("#4598 regression: drain does not merge images back")
	}
	if strings.Contains(body, "m.pendingImages = imgs\n") {
		t.Fatal("#4598 regression: replace-form write is back")
	}
}
