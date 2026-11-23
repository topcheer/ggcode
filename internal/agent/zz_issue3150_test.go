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
	if g == nil {
		t.Fatal("tied top suspects at CRS=30 must clear the threshold and attribute")
	}
	if !strings.Contains(g.step.filePath, "newdir/logo.png") {
		t.Fatalf("tie must break to the MORE RECENT edit (newdir/logo.png), got %+v", g.step)
	}
	if strings.Contains(g.step.filePath, "olddir/style.css") {
		t.Fatalf("older tied edit must not be the named suspect, got %+v", g.step)
	}
	if g.step.iteration != 2 {
		t.Fatalf("suspect should be the newer edit's step, got %+v", g.step)
	}
}
