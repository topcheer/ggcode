package agent

import "testing"

// zz_issue2976_test.go - regression probes for #2976: the tunnel-vision
// breadth gate computed uniqueSeen = len(filesTouched) + len(searchedFiles)
// - a SUM, not a union. A file that was read AND appeared in grep hits was
// counted twice; since grep hitting the files an agent is actively working
// on is the norm, the overlap is common. The issue's exact scenario: 3
// files read, ratio ~5.3 (16 iterations), grep hits exactly those 3 files
// -> sum = 6 >= 5 suppressed the warning the detector exists to fire; the
// union is 3 and the warning must fire.

func TestIssue2976FullOverlapNoLongerSuppresses(t *testing.T) {
	s := newTunnelVisionState()
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		s.recordFile(f)
		s.recordSearched(f) // grep hits exactly the files being read
	}
	// 16 iterations / 3 read files = ratio ~5.33 (>= 4.0), >= tvMinIterations.
	if msg := s.check(16); msg == "" {
		t.Fatalf("#2976: sum-as-union regression: full-overlap breadth (3 unique files) must not suppress the warning")
	}
}

func TestIssue2976PartialOverlapCountsUnion(t *testing.T) {
	// 3 read files + search hits covering those 3 plus 2 new ones:
	// union = 5 >= tvMinFilesForWarning -> still suppressed (legit breadth).
	s := newTunnelVisionState()
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		s.recordFile(f)
		s.recordSearched(f)
	}
	s.recordSearched("d.go")
	s.recordSearched("e.go")
	if msg := s.check(16); msg != "" {
		t.Fatalf("#2976: union of 5 must keep suppressing, got: %s", msg)
	}
}

func TestIssue2976ZeroOverlapUnchanged(t *testing.T) {
	// Disjoint sets: 3 read + 3 distinct search hits. Pre-fix behavior
	// (sum=6, suppressed) equals union behavior (6) - pins that the fix
	// changes nothing outside the overlap.
	s := newTunnelVisionState()
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		s.recordFile(f)
	}
	for _, f := range []string{"d.go", "e.go", "f.go"} {
		s.recordSearched(f)
	}
	if msg := s.check(16); msg != "" {
		t.Fatalf("#2976: disjoint breadth of 6 must stay suppressed, got: %s", msg)
	}
}
