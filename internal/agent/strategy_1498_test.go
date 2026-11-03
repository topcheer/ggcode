package agent

import "testing"

func Test1498StrategySignatureNormalized(t *testing.T) {
	a := seStrategySignature([]string{"read_file", "run_command"})
	b := seStrategySignature([]string{"run_command", "read_file"})
	if a != b {
		t.Fatal("order-insensitivity violated: {read,run} vs {run,read} must be one strategy")
	}
	if a != seStrategySignature([]string{"run_command"}) {
		t.Fatal("read-only tools must be dropped from the signature")
	}

	if seStrategySignature([]string{"edit_file", "edit_file"}) != seStrategySignature([]string{"edit_file"}) {
		t.Fatal("duplicate recovery tools must collapse")
	}

	if seStrategySignature([]string{"read_file", "grep", "glob"}) != seStrategySignature(nil) {
		t.Fatal("pure observation must equal the empty (no-strategy) signature")
	}

	if seStrategySignature([]string{"edit_file"}) == seStrategySignature([]string{"run_command"}) {
		t.Fatal("different recovery tools must remain distinct strategies")
	}
}

func Test1498StagnationThresholdThree(t *testing.T) {
	if stagnationFailureThreshold != 3 {
		t.Fatalf("threshold must be 3, got %d", stagnationFailureThreshold)
	}
	s := newStrategyStagnationState()
	tool, target := "run_command", "go get example.com/pkg"

	if s.recordAttempt(tool, target, false) {
		t.Fatal("first failure must not fire")
	}

	if s.recordAttempt(tool, target, false) {
		t.Fatal("second failure must not fire (verbatim transient retry)")
	}

	if !s.recordAttempt(tool, target, false) {
		t.Fatal("third consecutive identical failure must fire")
	}

	s2 := newStrategyStagnationState()
	s2.recordAttempt(tool, target, false)
	s2.recordAttempt(tool, target, false)
	if s2.recordAttempt(tool, target, true) {
		t.Fatal("success must reset the streak")
	}
}
