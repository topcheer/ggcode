package session

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// #4769 defect 3 (source-pin, per the #4524 precedent): removeFromIndex's
// final saveIndex failure path must mark the index dirty like updateIndex,
// or the self-healing reconciliation never fires and the deleted session
// lingers in the on-disk index forever.
func TestIssue4769_RemoveFromIndexFailureMarksDirty(t *testing.T) {
	b, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	anchor := regexp.MustCompile(`(?s)func \(s \*JSONLStore\) removeFromIndex.*?\n\}`)
	m := anchor.FindString(src)
	if m == "" {
		t.Fatal("removeFromIndex not found in store.go")
	}
	if strings.Contains(m, "return s.saveIndex(filtered)") {
		t.Fatal("removeFromIndex must check saveIndex's error, not return it bare (bare return skips the dirty flag on failure)")
	}
	if !strings.Contains(m, "saveIndex(filtered); err != nil") {
		t.Fatal("removeFromIndex must capture saveIndex's error")
	}
	if !strings.Contains(m, "setIndexDirty(true)") {
		t.Fatal("removeFromIndex saveIndex failure must setIndexDirty(true) for runMaintenance reconciliation")
	}
}
