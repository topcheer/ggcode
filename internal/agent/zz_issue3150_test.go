package agent

import (
	"strings"
	"testing"
)

func TestIssue3150_TieBreakPrefersMostRecentEdit(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "olddir/style.css", 1)
	s.recordEdit("edit_file", "newdir/logo.png", 2)

	output := "build failed\n" +
		"olddir/e1.go:1: undefined: A\nolddir/e2.go:2: undefined: B\n" +
		"olddir/e3.go:3: undefined: C\nolddir/e4.go:4: undefined: D\n" +
		"newdir/n1.go:1: undefined: E\nnewdir/n2.go:2: undefined: F"
	g := s.attributeFailure(output)
	if g == "" {
		t.Fatal("tied top suspects at CRS=30 must clear the threshold and produce guidance")
	}
	if !strings.Contains(g, "newdir/logo.png") {
		t.Fatalf("tie must break to the MORE RECENT edit (newdir/logo.png), got %q", g)
	}
	if strings.Contains(g, "olddir/style.css") {
		t.Fatalf("older tied edit must not be the named suspect, got %q", g)
	}
	if !strings.Contains(g, "step 2") {
		t.Fatalf("guidance should reference the newer edit's step number, got %q", g)
	}
}
