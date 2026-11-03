package agent

import "testing"

func TestIssue2976FullOverlapNoLongerSuppresses(t *testing.T) {
	s := newTunnelVisionState()
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		s.recordFile(f)
		s.recordSearched(f)
	}

	if msg := s.check(16); msg == "" {
		t.Fatalf("#2976: sum-as-union regression: full-overlap breadth (3 unique files) must not suppress the warning")
	}
}

func TestIssue2976PartialOverlapCountsUnion(t *testing.T) {

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
