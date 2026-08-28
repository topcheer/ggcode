package agent

// Regression tests for issue #1193 (same-family fixes already applied to
// param_count_check.go via #1149/#1187 but missed here):
//
//	A) The delta fingerprint keyed only on function name, so methods with the
//	   same name on DIFFERENT receivers ((s *Server) handle vs (c *Client)
//	   handle) collided and a newly added same-named method was silently
//	   absorbed by the old entry.
//	B) The Test/Benchmark exemption was not restricted to _test.go files, so
//	   production functions named TestXxx (e.g. TestConnection) were never
//	   checked.

import (
	"strings"
	"testing"
)

// issue1193SixReturnsTail is a 6-return function body (6 returns meets the
// returnCountThreshold) shared by the tests above.
const issue1193SixReturnsTail = `	if x == 1 { return 1 }
	if x == 2 { return 2 }
	if x == 3 { return 3 }
	if x == 4 { return 4 }
	if x == 5 { return 5 }
	return 0
`

// A) A NEW method with the same NAME but a different RECEIVER must be
// reported, not absorbed by the old entry's fingerprint.
func TestIssue1193_SameNameDifferentReceiver_NewMethodReported(t *testing.T) {
	old := `package main
type Server struct{}
type Client struct{}

func (s *Server) handle(x int) int {
` + issue1193SixReturnsTail + `}
`
	newContent := old + `
func (c *Client) handle(x int) int {
` + issue1193SixReturnsTail + `}
`
	warnings := checkExcessiveReturns("server.go", old, newContent)
	if len(warnings) == 0 {
		t.Fatal("newly added (c *Client) handle must be reported despite same-name (s *Server) handle existing (#1193A)")
	}
	if !strings.Contains(warnings[0], "handle") {
		t.Errorf("warning should mention the method name, got: %s", warnings[0])
	}
}

// A, control: the pre-existing same-receiver method is still delta-suppressed.
func TestIssue1193_SameReceiverSameName_StillSuppressed(t *testing.T) {
	src := `package main
type Server struct{}

func (s *Server) handle(x int) int {
` + issue1193SixReturnsTail + `}
`
	warnings := checkExcessiveReturns("server.go", src, src)
	if len(warnings) != 0 {
		t.Errorf("unchanged same-receiver method must be delta-suppressed, got: %v", warnings)
	}
}

// B) Test-prefixed functions in PRODUCTION code must be checked.
func TestIssue1193_ProductionTestPrefixedFunction_Checked(t *testing.T) {
	src := `package conn

func TestConnection(x int) int {
` + issue1193SixReturnsTail + `}
`
	warnings := checkExcessiveReturns("conn.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("production TestConnection must be checked - exemption must not apply outside _test.go (#1193B)")
	}
	if !strings.Contains(warnings[0], "TestConnection") {
		t.Errorf("warning should mention TestConnection, got: %s", warnings[0])
	}
}

// B) Test-prefixed functions in _test.go files remain exempt.
func TestIssue1193_TestFileTestFunction_StillExempt(t *testing.T) {
	src := `package conn

func TestConnectionFlow(t *testing.T) int {
` + issue1193SixReturnsTail + `}
`
	warnings := checkExcessiveReturns("conn_test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("Test function inside _test.go must stay exempt, got: %v", warnings)
	}
}

// A) Fingerprint is receiver-type based: identical method name on two
// receivers must yield distinct fingerprints.
func TestIssue1193_RcFingerprint_DistinctPerReceiver(t *testing.T) {
	a := returnCountInstance{funcName: "handle", recvType: "*Server"}
	b := returnCountInstance{funcName: "handle", recvType: "*Client"}
	if a.rcFingerprint() == b.rcFingerprint() {
		t.Errorf("different receivers must have distinct fingerprints: %q vs %q", a.rcFingerprint(), b.rcFingerprint())
	}
	c := returnCountInstance{funcName: "handle"}
	if c.rcFingerprint() == a.rcFingerprint() {
		t.Errorf("plain function must not collide with method fingerprint: %q", c.rcFingerprint())
	}
}
