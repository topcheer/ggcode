//go:build goolm

package agent

import "testing"

// TestIssue605_G4_WorseningWriteSurfaces: old=1 imbalance, new=2 — the
// boolean gate suppressed it (probe: zero warning); message-difference
// gating must surface the worsening write.
func TestIssue605_G4_WorseningWriteSurfaces(t *testing.T) {
	old := "func a() { if x {"
	new := "func a() { if x { if y {"
	w := checkDelimiterBalance("main.ts", new)
	if w == "" {
		t.Fatal("new content with 2 imbalances must warn")
	}
	oldW := checkDelimiterBalance("main.ts", old)
	if oldW == w {
		t.Fatalf("gate would suppress: identical messages %q", w)
	}
}

// TestIssue605_G4_UntouchedProblemStillSuppressed: same pre-existing
// imbalance in old and new (write touched other lines) stays suppressed.
func TestIssue605_G4_UntouchedProblemStillSuppressed(t *testing.T) {
	old := "func a() { if x {\n"
	new := "func a() { if x {\n// touched unrelated line\n"
	gated := deltaGateNew(checkDelimiterBalance)
	if got := gated(CheckContext{FilePath: "main.ts", OldContent: old, NewContent: new}); got != nil {
		t.Fatalf("untouched pre-existing problem must stay suppressed, got %v", got)
	}
}
