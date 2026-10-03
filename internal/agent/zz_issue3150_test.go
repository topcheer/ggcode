package agent

// #3150 probes.
//
// V1 (medium): a revert of an UNRELATED file must not flip the attribution
// experiment to intervened - a passing rerun would otherwise inject a false
// CONFIRMED verdict on an innocent change.
//
// V2 (low): when two edits tie on CRS score, the tie must break to the MORE
// RECENT edit (recency bias in causality, per the design comment), not the
// oldest one that happens to be first in append order.

import (
	"strings"
	"testing"
)

// V1: unrelated git restore / pathspec-limited stash must not count as an
// intervention, so the passing rerun stays silent instead of confirming.
func TestIssue3150_UnrelatedRevert_NoFalseConfirmed(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
	}{
		{"restore other file", "git restore internal/agent/unrelated.go"},
		{"checkout-- other file", "git checkout -- internal/agent/unrelated.go"},
		{"stash push pathspec other file", "git stash push -- internal/agent/unrelated.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := r405Arm(t)
			if g := s.observeCommand("run_command", tc.cmd, false, ""); g != "" {
				t.Fatalf("unrelated revert observation must be silent, got %q", g)
			}
			if g := s.observeCommand("run_command", r405Verify, false, "ok  all tests passed"); g != "" {
				t.Fatalf("unrelated revert must not arm a CONFIRMED verdict, got %q", g)
			}
		})
	}
}

// V1 true-positive preservation: reverts that DO hit the suspect (named
// pathspec, bare stash, stash push without pathspec) still arm the verdict.
func TestIssue3150_SuspectRevert_StillConfirms(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
	}{
		{"restore suspect", "git restore " + r405Suspect},
		{"stash push pathspec suspect", "git stash push -- " + r405Suspect},
		{"bare stash", "git stash"},
		{"stash push with message, no pathspec", "git stash push -m \"wip: debugging\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := r405Arm(t)
			s.observeCommand("run_command", tc.cmd, false, "")
			if g := s.observeCommand("run_command", r405Verify, false, "ok  all tests passed"); !strings.Contains(g, "CONFIRMED") {
				t.Fatalf("suspect-hitting revert must still arm CONFIRMED, got %q", g)
			}
		})
	}
}

// V1 boundary: "git stash push -- " with nothing after the separator is a
// bare push in effect (empty pathspec), not a pathspec-limited one.
func TestIssue3150_StashPushTrailingSeparator_CountsAsBare(t *testing.T) {
	s := r405Arm(t)
	s.observeCommand("run_command", "git stash push -- ", false, "")
	if g := s.observeCommand("run_command", r405Verify, false, "ok"); !strings.Contains(g, "CONFIRMED") {
		t.Fatalf("empty-pathspec stash push reverts everything, must count, got %q", g)
	}
}

// V2: two edits tie on CRS (same-dir weight offsets the recency gap), the
// top suspect must be the NEWER edit.
//
// Tie construction: error files web/err1.go + web/err2.go (same dir, no
// exact match with any edit). Edit A (web/app.css, non-Go so same-package
// weight never applies) earns same-dir 5x2 = 10 at rank 2 -> 10+20 = 30.
// Edit B (assets/logo.png, unrelated) earns 0 at rank 3 -> 0+30 = 30.
// C is an unrelated filler that only lifts the ranks.
func TestIssue3150_TieBreakPrefersMostRecentEdit(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "docs/readme.md", 1)  // filler: rank 1, score 10
	s.recordEdit("edit_file", "web/app.css", 2)     // older suspect: rank 2, score 30
	s.recordEdit("edit_file", "assets/logo.png", 3) // newer suspect: rank 3, score 30

	output := "build failed\nweb/err1.go:1: undefined: Foo\nweb/err2.go:2: undefined: Bar"
	g := s.attributeFailure(output)
	if g == "" {
		t.Fatal("tied top suspects at CRS=30 must clear the threshold and produce guidance")
	}
	if !strings.Contains(g, "assets/logo.png") {
		t.Fatalf("tie must break to the MORE RECENT edit (assets/logo.png), got %q", g)
	}
	if strings.Contains(g, "web/app.css") {
		t.Fatalf("older tied edit must not be the named suspect, got %q", g)
	}
	if !strings.Contains(g, "step 3") {
		t.Fatalf("guidance should reference the newer edit's step number, got %q", g)
	}
}
