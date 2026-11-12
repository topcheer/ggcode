package agent

import (
	"strings"
	"testing"
)

func TestFix3990FixIntentOverridesResearchVerbs(t *testing.T) {
	mixedTasks := []string{

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

	pureResearch := []string{
		"Investigate memory leak in goroutines",
		"Analyze the codebase for security gaps",
		"Assess the impact of the refactoring",
	}
	for _, task := range pureResearch {
		if !detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = false, want true (pure research)", task)
		}
	}
}

func TestFix3990FileStuckRaisedNotDisabledInResearchMode(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true

	for i := 0; i < 4; i++ {
		o.recordToolCall("read_file", false, "auth.go", "", i)
	}
	if msg := o.checkFileStuck(nil); msg != "" {
		t.Fatalf("file_stuck fired below research threshold: %s", msg)
	}

	for i := 0; i < 4; i++ {
		o.recordToolCall("read_file", false, "auth.go", "", i)
	}
	msg := o.checkFileStuck(nil)
	if msg == "" {
		t.Fatal("file_stuck must fire in research mode at the raised threshold (detector must not be disabled)")
	}
	if !strings.Contains(msg, "auth.go") || !strings.Contains(msg, "8") {
		t.Fatalf("unexpected hint content: %s", msg)
	}
}

func TestFix3989StartCommandExcludedFromSuccessfulCommands(t *testing.T) {
	src, err := readAgentAndProcessingSources()
	if err != nil {
		t.Skipf("cannot read agent.go source: %v", err)
	}
	srcStr := string(src)

	if !strings.Contains(srcStr, `if tc.Name == "run_command" && !result.IsError {`) ||
		!strings.Contains(srcStr, "runStats.recordSuccessfulCommand(cmd)") {
		t.Fatal("recordSuccessfulCommand must be gated on tc.Name == \"run_command\" (#3989): " +
			"a successful start_command spawn would be persisted as a verified cmd_snippet")
	}

}
