package knight

import (
	"strings"
	"testing"
)

func TestIssue1617_RenameBothSides(t *testing.T) {
	snap := "R  .ggcode/a.md -> .ggcode/b.md\n" +
		"R  tracked.txt -> .ggcode/hidden.md\n" +
		"R  .ggcode/x.md -> outside.md\n" +
		"?? .ggcode/new.md\n"
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
