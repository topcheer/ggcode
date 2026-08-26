package agent

import (
	"strings"
	"testing"
)

// TestIssue1108_ForInitLoopDowngradedOnGo122 guards #1108: Go 1.22
// per-iteration semantics cover BOTH range and for-init declared variables
// (per the Go 1.22 release notes), so the classic-gotcha warning must be
// downgraded for for-loops as well, not only range loops.
func TestIssue1108_ForInitLoopDowngradedOnGo122(t *testing.T) {
	restore := isGo122Plus
	isGo122Plus = true
	defer func() { isGo122Plus = restore }()

	inst := loopCaptureInstance{
		varName:  "i",
		kind:     "goroutine",
		loopType: "for",
	}
	msg := formatLoopCaptureWarning(inst)
	if strings.Contains(msg, "classic Go gotcha") {
		t.Fatalf("for-init loop capture must be downgraded on Go 1.22+ (#1108), got: %s", msg)
	}

	// Sanity: with Go < 1.22 the full gotcha warning stays.
	isGo122Plus = false
	msg = formatLoopCaptureWarning(inst)
	if !strings.Contains(msg, "classic Go gotcha") {
		t.Fatalf("pre-1.22 must keep the classic gotcha warning, got: %s", msg)
	}
}
