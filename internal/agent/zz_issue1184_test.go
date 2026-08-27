package agent

import "testing"

// Regression test for GitHub issue #1184: the param_count delta
// fingerprint keyed on parameter NAMES, so a pure parameter rename of an
// already-flagged 6+ param function (name/count/types all unchanged)
// made every old key miss and re-reported the pre-existing instance as
// newly introduced - violating the file's own "only flags NEW instances
// introduced by this edit" contract. Same delta-contract family as
// #1179. The fingerprint must be rename-stable (types + count).
func TestIssue1184_PureParamRenameDoesNotReReport(t *testing.T) {
	oldSrc := `package p

func process(a, b, c, d, e, f int) error {
	return nil
}
`
	// Pure rename: a -> x. Same smell, same count, same types.
	newSrc := `package p

func process(x, b, c, d, e, f int) error {
	return nil
}
`
	warnings := checkExcessiveParams("test.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Fatalf("pure parameter rename re-reported pre-existing instance: %v", warnings)
	}
}

func TestIssue1184_PureReceiverRenameDoesNotReReport(t *testing.T) {
	oldSrc := `package p

type Server struct{}

func (s *Server) handle(a, b, c, d, e, f int) error {
	return nil
}
`
	// Pure receiver rename: s -> srv. recvType keys on the TYPE, and the
	// receiver NAME previously leaked into the key via paramNames(fn.Recv).
	newSrc := `package p

type Server struct{}

func (srv *Server) handle(a, b, c, d, e, f int) error {
	return nil
}
`
	warnings := checkExcessiveParams("test.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Fatalf("pure receiver rename re-reported pre-existing instance: %v", warnings)
	}
}

func TestIssue1184_GenuinelyNewParamStillReported(t *testing.T) {
	oldSrc := `package p

func process(a, b, c, d, e int) error {
	return nil
}
`
	// Real change: 5 -> 6 params crosses the threshold; must be reported.
	newSrc := `package p

func process(a, b, c, d, e, f int) error {
	return nil
}
`
	warnings := checkExcessiveParams("test.go", oldSrc, newSrc)
	if len(warnings) == 0 {
		t.Fatalf("genuinely new 6-param instance must still be reported")
	}
}

func TestIssue1184_TypeChangeStillReported(t *testing.T) {
	oldSrc := `package p

func process(a, b, c, d, e string) error {
	return nil
}
`
	// Type change alters the semantic identity; a 6th param of a DIFFERENT
	// type than any old entry must not be absorbed by the old fingerprint.
	newSrc := `package p

func process(a, b, c, d, e, f int) error {
	return nil
}
`
	warnings := checkExcessiveParams("test.go", oldSrc, newSrc)
	if len(warnings) == 0 {
		t.Fatalf("type-changing 6th param must be reported as new")
	}
}
