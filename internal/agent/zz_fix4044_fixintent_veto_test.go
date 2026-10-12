package agent

import "testing"

// TestFix4044FixIntentVetoPositionSensitive: #4044 - the fix-intent veto
// must only fire when the fix verb commands the task (imperative sentence
// start, or a coordinated follow-on clause with a determiner object).
// Research wording containing fix-family nouns or subordinate-clause verbs
// must stay in research mode.
func TestFix4044FixIntentVetoPositionSensitive(t *testing.T) {
	// Issue repro table: read-only research tasks previously misclassified
	// as fix intent must return to research mode.
	research := []string{
		"Compare how different databases resolve deadlocks",
		"Survey of automated patch generation techniques",
		"Research how open-source projects fix concurrency bugs",
		"Investigate how Go's GC fixes memory leaks (read-only survey)",
		"Study how teams apply patches in production",      // noun plural
		"Analyze the refactoring, fixes, and patches used", // noun list; no determiner object
	}
	for _, task := range research {
		if !detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = false, want true (research task wrongly vetoed)", task)
		}
	}

	// Genuine fix intent must still veto research verbs (#3990 contract).
	fixTasks := []string{
		"Investigate why tests fail and fix it",
		"Review the diff and identify the bug, then fix",
		"Analyze the stack trace and repair the handler",
		"Audit the config loading and resolve the startup crash",
		"Explore the failing test, implement the missing stub",
		"Fix the failing test in parser.go",
		"Please fix the login bug on mobile",
		"Research the crash first. Resolve the null pointer afterwards",
	}
	for _, task := range fixTasks {
		if detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = true, want false (fix intent must override research verbs)", task)
		}
	}
}
