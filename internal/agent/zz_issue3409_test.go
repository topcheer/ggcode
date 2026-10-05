package agent

// #3409 probes: tools outside extractStagnationTarget's switch yield an
// empty target; the old code still recorded them, and the tail-match
// (toolName && target) degenerates when both sides are "" - distinct calls
// of the same uncovered tool collapse onto one trajectory, so failures on
// different real targets falsely chain into a stagnation warning, and any
// success erases a genuinely-stuck chain (互消). Covered tools keep the
// same-tool+same-target chaining semantics.

import "testing"

func TestIssue3409_UncoveredToolDoesNotFalselyChain(t *testing.T) {
	s := newStrategyStagnationState()
	// mcp__x is outside the switch: distinct real targets, all failures.
	// Old behavior: target "" chained them and fired a false warning.
	for i := 0; i < stagnationFailureThreshold+1; i++ {
		if s.recordAttempt("mcp__x", `{"foo":"bar"+it}`, false) {
			t.Fatal("uncovered tool must not fire stagnation warnings (empty-target bucket is degenerate)")
		}
	}
}

func TestIssue3409_UncoveredSuccessDoesNotEraseStuckChain(t *testing.T) {
	s := newStrategyStagnationState()
	// Build a genuinely-stuck covered chain: same edit_file target, failing.
	for i := 0; i < stagnationFailureThreshold; i++ {
		s.recordAttempt("edit_file", `{"file_path":"/a.go"}`, false)
	}
	// An uncovered-tool success between failures previously landed in the
	// "" bucket of the SAME window and, for tools sharing the empty target,
	// broke the consecutive-failure tail scan for the covered chain's
	// subsequent comparison window.
	s.recordAttempt("spawn_agent", `{"task":"unrelated"}`, true)
	if !s.recordAttempt("edit_file", `{"file_path":"/a.go"}`, false) {
		t.Fatal("covered same-target failure chain must still fire after an uncovered-tool interleave")
	}
}

func TestIssue3409_CoveredChainingUnchanged(t *testing.T) {
	s := newStrategyStagnationState()
	for i := 0; i < stagnationFailureThreshold-1; i++ {
		if s.recordAttempt("edit_file", `{"file_path":"/a.go"}`, false) {
			t.Fatal("below threshold must not fire")
		}
	}
	if !s.recordAttempt("edit_file", `{"file_path":"/a.go"}`, false) {
		t.Fatal("at threshold, same tool+target consecutive failures must fire")
	}
	// A success on the same target breaks the chain.
	if s.recordAttempt("edit_file", `{"file_path":"/a.go"}`, true) {
		t.Fatal("success must not fire")
	}
	if s.recordAttempt("edit_file", `{"file_path":"/a.go"}`, false) {
		t.Fatal("chain was broken by success; single failure must not fire")
	}
}
