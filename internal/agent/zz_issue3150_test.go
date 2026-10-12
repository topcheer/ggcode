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

// V2: two edits tie on CRS, the top suspect must be the NEWER edit.
//
// #3794-B contract: the recency bonus now requires the edit to have
// actually matched at least one error reference (file/dir/package tier),
// so a zero-evidence edit can no longer manufacture a tie via recency
// alone (the pre-#3794 construction - edit B unrelated, 0 evidence +
// recency 30 - now correctly scores 0). The tie is therefore built from
// evidence on BOTH sides, with the older edit carrying more dir evidence
// to offset its lower recency rank:
//
// Edit A (olddir/style.css, rank 1): 4 same-dir error hits 5x4 = 20
//   - recency 1x10 = 10 -> 30.
//
// Edit B (newdir/logo.png, rank 2): 2 same-dir error hits 5x2 = 10
//   - recency 2x10 = 20 -> 30.
//
// Both suspects carry real evidence; equal score must resolve to B.
func TestIssue3150_TieBreakPrefersMostRecentEdit(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "olddir/style.css", 1) // older suspect: rank 1, score 30
	s.recordEdit("edit_file", "newdir/logo.png", 2)  // newer suspect: rank 2, score 30

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
