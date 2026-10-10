package agent

// #3862 probes: make availability probed via PATH, ruff pattern anchored on
// a path-like first token, and both -list spellings excluded from real-test
// segments.

import (
	"os/exec"
	"strings"
	"testing"
)

func TestIssue3862_MakeAvailabilityProbedNotAssumed(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make unavailable - the probe path IS the fix")
	}
	_ = makePath
	if !lintCommandAvailable(t.TempDir(), "make lint") {
		t.Fatal("make present in PATH must be reported available")
	}
	// Non-make commands keep their existing behavior (unknown tool).
	if lintCommandAvailable(t.TempDir(), "definitely_not_a_tool_xyz") {
		t.Fatal("unknown tool must stay unavailable")
	}
}

func TestIssue3862_TimingLinesNotRuff(t *testing.T) {
	for _, s := range []string{
		"duration: 0:00:05",
		"total: 12:34:56",
		"uptime: 1:02:03",
	} {
		if hasRuffPattern(s) {
			t.Fatalf("timing line must not look like a ruff warning: %q", s)
		}
	}
	// Real ruff lines still match.
	for _, s := range []string{
		"pkg/mod.py:12:5: F841 local variable assigned but never used",
		"src/a.b.py:1:2: E501 line too long",
	} {
		if !hasRuffPattern(s) {
			t.Fatalf("real ruff line must match: %q", s)
		}
	}
}

func TestIssue3862_ListBothSpellingsExcluded(t *testing.T) {
	if isRealTestSegment("go test ./... -list .") {
		t.Fatal("single-dash -list only lists test names - not a real execution")
	}
	if isRealTestSegment("go test ./... --list .") {
		t.Fatal("double-dash --list must stay excluded")
	}
	// A real run keeps counting.
	if !isRealTestSegment("go test ./...") {
		t.Fatal("plain go test must remain a real execution")
	}
	_ = strings.Contains
}
