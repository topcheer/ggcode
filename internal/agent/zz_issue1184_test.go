package agent

import "testing"

func TestIssue1184_PureParamRenameDoesNotReReport(t *testing.T) {
	oldSrc := `package p

func process(a, b, c, d, e, f int) error {
	return nil
}
`

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
