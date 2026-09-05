package knight

import (
	"strings"
	"testing"
)

// TestIssue1617_RenameBothSides pins #1617-A: only renames fully INSIDE
// .ggcode are bookkeeping; either side crossing the boundary stays
// visible to the read-only guard.
func TestIssue1617_RenameBothSides(t *testing.T) {
	snap := "R  .ggcode/a.md -> .ggcode/b.md\n" + // internal: filtered
		"R  tracked.txt -> .ggcode/hidden.md\n" + // stash channel: KEPT (#1617-A)
		"R  .ggcode/x.md -> outside.md\n" + // lands outside: KEPT (#1576-C)
		"?? .ggcode/new.md\n" // plain internal add: filtered
	got := filterKnightBookkeeping(snap)
	if strings.Contains(got, ".ggcode/a.md") {
		t.Error("internal rename must stay filtered")
	}
	if !strings.Contains(got, "tracked.txt") {
		t.Error("outside->.ggcode rename must stay VISIBLE (stash channel)")
	}
	if !strings.Contains(got, "outside.md") {
		t.Error(".ggcode->outside rename must stay visible (#1576-C)")
	}
	if strings.Contains(got, "?? .ggcode/new.md") {
		t.Error("plain internal add must stay filtered")
	}
}
