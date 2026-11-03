package agent

import "testing"

func TestIssue3409_UncoveredToolDoesNotFalselyChain(t *testing.T) {
	s := newStrategyStagnationState()

	for i := 0; i < stagnationFailureThreshold+1; i++ {
		if s.recordAttempt("mcp__x", `{"foo":"bar"+it}`, false) {
			t.Fatal("uncovered tool must not fire stagnation warnings (empty-target bucket is degenerate)")
		}
	}
}

func TestIssue3409_UncoveredSuccessDoesNotEraseStuckChain(t *testing.T) {
	s := newStrategyStagnationState()

	for i := 0; i < stagnationFailureThreshold; i++ {
		s.recordAttempt("edit_file", `{"file_path":"/a.go"}`, false)
	}

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

	if s.recordAttempt("edit_file", `{"file_path":"/a.go"}`, true) {
		t.Fatal("success must not fire")
	}
	if s.recordAttempt("edit_file", `{"file_path":"/a.go"}`, false) {
		t.Fatal("chain was broken by success; single failure must not fire")
	}
}
