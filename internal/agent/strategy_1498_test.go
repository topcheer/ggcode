package agent

import "testing"

// #1498 case C: strategy signatures must be order-insensitive and drop
// read-only observation tools - {read,run} and {run,read} are ONE strategy,
// and pure read noise must not fabricate distinct strategies.
func Test1498StrategySignatureNormalized(t *testing.T) {
	a := seStrategySignature([]string{"read_file", "run_command"})
	b := seStrategySignature([]string{"run_command", "read_file"})
	if a != b {
		t.Fatal("order-insensitivity violated: {read,run} vs {run,read} must be one strategy")
	}
	if a != seStrategySignature([]string{"run_command"}) {
		t.Fatal("read-only tools must be dropped from the signature")
	}
	// Duplicates collapse (a set, not a multiset).
	if seStrategySignature([]string{"edit_file", "edit_file"}) != seStrategySignature([]string{"edit_file"}) {
		t.Fatal("duplicate recovery tools must collapse")
	}
	// All-read between errors is not a strategy at all (matches empty sig).
	if seStrategySignature([]string{"read_file", "grep", "glob"}) != seStrategySignature(nil) {
		t.Fatal("pure observation must equal the empty (no-strategy) signature")
	}
	// Genuinely different recovery sets stay distinct.
	if seStrategySignature([]string{"edit_file"}) == seStrategySignature([]string{"run_command"}) {
		t.Fatal("different recovery tools must remain distinct strategies")
	}
}

// #1498 case D: stagnation fires on the THIRD consecutive same-tool+target
// failure, not the second (a verbatim transient retry is endorsed recovery,
// not a rut).
func Test1498StagnationThresholdThree(t *testing.T) {
	if stagnationFailureThreshold != 3 {
		t.Fatalf("threshold must be 3, got %d", stagnationFailureThreshold)
	}
	s := newStrategyStagnationState()
	tool, target := "run_command", "go get example.com/pkg"
	// First failure: no fire.
	if s.recordAttempt(tool, target, false) {
		t.Fatal("first failure must not fire")
	}
	// Second (verbatim retry, transient-recovery-endorsed): no fire.
	if s.recordAttempt(tool, target, false) {
		t.Fatal("second failure must not fire (verbatim transient retry)")
	}
	// Third identical failure: fire.
	if !s.recordAttempt(tool, target, false) {
		t.Fatal("third consecutive identical failure must fire")
	}
	// A success resets the streak.
	s2 := newStrategyStagnationState()
	s2.recordAttempt(tool, target, false)
	s2.recordAttempt(tool, target, false)
	if s2.recordAttempt(tool, target, true) {
		t.Fatal("success must reset the streak")
	}
}
