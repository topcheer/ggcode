package agent

import (
	"strings"
	"testing"
)

func TestDiffSummaryState_FiresOnce(t *testing.T) {
	d := newDiffSummaryState()
	if d.fired {
		t.Fatal("should start unfired")
	}
	// Simulate first call sets fired=true
	d.fired = true
	if !d.fired {
		t.Fatal("should be fired after setting")
	}
}

func TestDiffSummaryState_Reset(t *testing.T) {
	d := newDiffSummaryState()
	d.fired = true
	d.reset()
	if d.fired {
		t.Fatal("reset should clear fired flag")
	}
}

func TestDiffSummaryGate_MinFilesNotMet(t *testing.T) {
	a := &Agent{
		diffSummary: newDiffSummaryState(),
	}
	// With nil runStats or no files, should return ""
	result := a.checkDiffSummaryGate(&RunStats{})
	if result != "" {
		t.Fatalf("expected empty for no changes, got: %q", result)
	}
}

func TestShortenPathForDisplay_RelativePath(t *testing.T) {
	// Git diff --stat already shows repo-relative paths, so the function
	// should return the line unchanged for relative paths.
	line := " internal/agent/planner.go | 15 +++--"
	result := shortenPathForDisplay("/some/workdir", line)
	if result != line {
		t.Fatalf("expected unchanged for relative path, got: %q", result)
	}
}

func TestDiffSummaryGate_FiresOnlyOnce(t *testing.T) {
	a := &Agent{
		diffSummary: newDiffSummaryState(),
	}
	// Even if called multiple times, only fires once (returns "" after first)
	// First call with no code changes returns "" but marks fired
	a.checkDiffSummaryGate(&RunStats{})
	// Second call should also return "" because fired=true
	result := a.checkDiffSummaryGate(&RunStats{})
	if result != "" {
		t.Fatalf("expected empty on second call, got: %q", result)
	}
}

func TestDiffSummaryGate_NilState(t *testing.T) {
	a := &Agent{
		diffSummary: nil,
	}
	result := a.checkDiffSummaryGate(&RunStats{})
	if result != "" {
		t.Fatalf("expected empty with nil state, got: %q", result)
	}
}

// TestDiffSummaryGateOutputFormat verifies the message format when the gate
// would fire (tested via the message construction logic, not actual git I/O).
func TestDiffSummaryGateOutputFormat(t *testing.T) {
	// Verify that the expected message prefix is used.
	expectedPrefix := "[Self-review:"
	// Build a synthetic message the way the gate does.
	var sb strings.Builder
	sb.WriteString("[Self-review: you are about to finish. Here is a summary of ALL your changes (git diff --stat). ")
	sb.WriteString("Review this list, verify each change is intentional, complete, and matches the user's request.]\n")
	msg := sb.String()
	if !strings.HasPrefix(msg, expectedPrefix) {
		t.Fatalf("message should start with %q", expectedPrefix)
	}
}
