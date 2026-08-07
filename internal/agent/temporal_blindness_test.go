package agent

import (
	"testing"
)

func TestTemporalBlindness_NoVerification(t *testing.T) {
	tb := newTemporalBlindnessState()
	// No verification recorded yet.
	hint := tb.maybeWarnTemporalBlindness("the build passes")
	if hint != "" {
		t.Fatalf("expected no warning without prior verification, got: %s", hint)
	}
}

func TestTemporalBlindness_InsufficientMutations(t *testing.T) {
	tb := newTemporalBlindnessState()
	tb.recordVerification("run_command", "", "build successful", 1)
	// Only 2 mutations - below threshold of 3.
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	hint := tb.maybeWarnTemporalBlindness("the build passes")
	if hint != "" {
		t.Fatalf("expected no warning with only 2 mutations, got: %s", hint)
	}
}

func TestTemporalBlindness_DetectsStaleClaim(t *testing.T) {
	tb := newTemporalBlindnessState()
	tb.recordVerification("run_command", "", "build successful", 1)
	// 3 mutations - at threshold.
	tb.recordMutation("edit_file")
	tb.recordMutation("write_file")
	tb.recordMutation("edit_file")
	hint := tb.maybeWarnTemporalBlindness("the build passes")
	if hint == "" {
		t.Fatal("expected warning for stale verification claim after 3 mutations")
	}
	if !contains(hint, "temporal-blindness") {
		t.Fatalf("expected [temporal-blindness] tag, got: %s", hint)
	}
}

func TestTemporalBlindness_NoClaimNoWarning(t *testing.T) {
	tb := newTemporalBlindnessState()
	tb.recordVerification("run_command", "", "build successful", 1)
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	// No verification claim in text - should not warn.
	hint := tb.maybeWarnTemporalBlindness("I changed the function signature")
	if hint != "" {
		t.Fatalf("expected no warning without verification claim, got: %s", hint)
	}
}

func TestTemporalBlindness_ReVerificationResetsMutations(t *testing.T) {
	tb := newTemporalBlindnessState()
	tb.recordVerification("run_command", "", "build successful", 1)
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	// Re-verify - resets mutation count.
	tb.recordVerification("run_command", "", "all tests pass", 5)
	hint := tb.maybeWarnTemporalBlindness("the build passes")
	if hint != "" {
		t.Fatalf("expected no warning after re-verification, got: %s", hint)
	}
}

func TestTemporalBlindness_MaxWarnings(t *testing.T) {
	tb := newTemporalBlindnessState()
	tb.recordVerification("run_command", "", "build successful", 1)
	for i := 0; i < 5; i++ {
		tb.recordMutation("edit_file")
	}
	tb.maybeWarnTemporalBlindness("the build passes")
	// More mutations to trigger re-warn eligibility.
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	hint2 := tb.maybeWarnTemporalBlindness("all tests pass")
	if hint2 == "" {
		t.Fatal("expected second warning")
	}
	// Third warning should be suppressed.
	for i := 0; i < 3; i++ {
		tb.recordMutation("edit_file")
	}
	hint3 := tb.maybeWarnTemporalBlindness("build is green")
	if hint3 != "" {
		t.Fatalf("expected suppression after 2 warnings, got: %s", hint3)
	}
}

func TestTemporalBlindness_NonVerificationToolIgnored(t *testing.T) {
	tb := newTemporalBlindnessState()
	// grep is not a verification tool.
	tb.recordVerification("grep", "", "found 5 matches", 1)
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	hint := tb.maybeWarnTemporalBlindness("the build passes")
	if hint != "" {
		t.Fatalf("expected no warning for non-verification tool, got: %s", hint)
	}
}

func TestTemporalBlindness_NonMutationToolIgnored(t *testing.T) {
	tb := newTemporalBlindnessState()
	tb.recordVerification("run_command", "", "build successful", 1)
	// read_file, grep - not mutations.
	tb.recordMutation("read_file")
	tb.recordMutation("grep")
	tb.recordMutation("read_file")
	hint := tb.maybeWarnTemporalBlindness("the build passes")
	if hint != "" {
		t.Fatalf("expected no warning for non-mutation tools, got: %s", hint)
	}
}

func TestTemporalBlindness_VariousStalenessClaims(t *testing.T) {
	claims := []string{
		"the build passes",
		"all tests pass",
		"as verified earlier",
		"verification confirms correctness",
		"lint is clean",
		"the test is green",
		"compilation passed",
	}
	for _, claim := range claims {
		tb := newTemporalBlindnessState()
		tb.recordVerification("run_command", "", "build successful", 1)
		tb.recordMutation("edit_file")
		tb.recordMutation("edit_file")
		tb.recordMutation("edit_file")
		hint := tb.maybeWarnTemporalBlindness(claim)
		if hint == "" {
			t.Errorf("expected warning for claim %q, got none", claim)
		}
	}
}

func TestTemporalBlindness_Reset(t *testing.T) {
	tb := newTemporalBlindnessState()
	tb.recordVerification("run_command", "", "build successful", 1)
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	tb.recordMutation("edit_file")
	tb.maybeWarnTemporalBlindness("the build passes")
	tb.reset()
	if tb.lastVerifiedSeq != 0 || tb.mutationCount != 0 || tb.warningCount != 0 {
		t.Fatal("reset did not clear state")
	}
}
