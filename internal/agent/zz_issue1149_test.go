package agent

import "testing"

// Guards #1149: the delta suppression must be multiset-based and the method
// fingerprint must key on the receiver TYPE, not the receiver variable name.

func TestIssue1149_SecondIdenticalClosureReported(t *testing.T) {
	old := `package p
func use() {
	handler := func(a, b, c, d, e, f int) int { return a + f }
	_ = handler
}`
	new := `package p
func use() {
	handler := func(a, b, c, d, e, f int) int { return a + f }
	_ = handler
	processor := func(a, b, c, d, e, f int) int { return b + c }
	_ = processor
}`
	warns := checkExcessiveParams("a.go", old, new)
	if len(warns) != 1 {
		t.Fatalf("new identical-signature closure must be reported once (old count consumed), got %d: %v", len(warns), warns)
	}
}

func TestIssue1149_SameNameMethodDifferentReceiverTypeReported(t *testing.T) {
	old := `package p
type Server struct{}
func (s *Server) handle(a, b, c, d, e int) {}
`
	new := `package p
type Server struct{}
type Client struct{}
func (s *Server) handle(a, b, c, d, e int) {}
func (s *Client) handle(a, b, c, d, e int) {}
`
	warns := checkExcessiveParams("a.go", old, new)
	if len(warns) != 1 {
		t.Fatalf("(s *Server) handle and (s *Client) handle must not collide on the receiver variable name; expected 1 new-instance warning, got %d: %v", len(warns), warns)
	}
}

func TestIssue1149_MultisetOldCountFullyConsumed(t *testing.T) {
	old := `package p
func use() {
	f1 := func(a, b, c, d, e, f int) int { return a }
	f2 := func(a, b, c, d, e, f int) int { return b }
	_, _ = f1, f2
}`
	new := old // unchanged file: line-shift safety net (#1142) must hold
	warns := checkExcessiveParams("a.go", old, new)
	if len(warns) != 0 {
		t.Fatalf("unchanged identical instances must stay silent, got %d: %v", len(warns), warns)
	}
}

func TestIssue1149_LineShiftStillSilent(t *testing.T) {
	old := `package p

func big(a, b, c, d, e int) {}
`
	new := `package p

// a comment inserted above shifts every position (#1142)
func big(a, b, c, d, e int) {}
`
	warns := checkExcessiveParams("a.go", old, new)
	if len(warns) != 0 {
		t.Fatalf("pure line shift must not re-report, got %d: %v", len(warns), warns)
	}
}
