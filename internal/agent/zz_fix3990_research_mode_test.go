package agent

import (
	"os"
	"strings"
	"testing"
)

// #3990: research verbs are ordinary wording in bug-fix tasks. A task that
// states a fix deliverable must NOT enter research mode, or the four stuck
// detectors are disabled/loosened for the whole run - including file_stuck,
// which targets exactly the implementation-phase stall.
func TestFix3990FixIntentOverridesResearchVerbs(t *testing.T) {
	mixedTasks := []string{
		// The issue's canonical examples: research verb + fix deliverable.
		"Investigate why tests fail and fix it",
		"Review the diff and identify the bug, then fix",
		"Analyze the stack trace and repair the handler",
		"Audit the config loading and resolve the startup crash",
		"Explore the failing test, implement the missing stub",
	}
	for _, task := range mixedTasks {
		if detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = true, want false (fix intent must override research verbs)", task)
		}
	}
	// Pure research tasks must stay in research mode.
	pureResearch := []string{
		"Investigate memory leak in goroutines",
		"Analyze the codebase for security gaps",
		"Assess the impact of the refactoring", // contains "refactoring" but no fix verb
	}
	for _, task := range pureResearch {
		if !detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = false, want true (pure research)", task)
		}
	}
}

// #3990: in research mode file_stuck must be RAISED (4 -> 8), not disabled.
func TestFix3990FileStuckRaisedNotDisabledInResearchMode(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	// 4 reads: below the research threshold, no hint yet (normal re-reading
	// during analysis).
	for i := 0; i < 4; i++ {
		o.recordToolCall("read_file", false, "auth.go", "")
	}
	if msg := o.checkFileStuck(nil); msg != "" {
		t.Fatalf("file_stuck fired below research threshold: %s", msg)
	}
	// 8 reads: the hint must fire even in research mode - a disabled
	// detector is blind to a genuine same-file circle.
	for i := 0; i < 4; i++ {
		o.recordToolCall("read_file", false, "auth.go", "")
	}
	msg := o.checkFileStuck(nil)
	if msg == "" {
		t.Fatal("file_stuck must fire in research mode at the raised threshold (detector must not be disabled)")
	}
	if !strings.Contains(msg, "auth.go") || !strings.Contains(msg, "8") {
		t.Fatalf("unexpected hint content: %s", msg)
	}
}

// #3989: recordSuccessfulCommand must only see verified command results.
// start_command's IsError=false only proves the background job spawned, not
// that the inner command passed, so a successful spawn must NOT be distilled
// into a "verified-successful" cmd_snippet. The gate lives inline in the
// executeTools loop (no cheap loop harness exists), so this pins the source
// contract the way #3682's adjacent scopeNarrow exclusion is documented.
func TestFix3989StartCommandExcludedFromSuccessfulCommands(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Skipf("cannot read agent.go source: %v", err)
	}
	srcStr := string(src)
	// The recording call must be gated on run_command only.
	if !strings.Contains(srcStr, `if tc.Name == "run_command" && !result.IsError {
						runStats.recordSuccessfulCommand(cmd)`) {
		t.Fatal("recordSuccessfulCommand must be gated on tc.Name == \"run_command\" (#3989): " +
			"a successful start_command spawn would be persisted as a verified cmd_snippet")
	}
	// And the gate must come with the #3989 rationale comment.
	if !strings.Contains(srcStr, "#3989: start_command is EXCLUDED") {
		t.Fatal("the #3989 exclusion rationale comment is missing next to the gate")
	}
}
