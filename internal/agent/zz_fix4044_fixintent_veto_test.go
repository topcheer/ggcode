package agent

import "testing"

func TestFix4044FixIntentVetoPositionSensitive(t *testing.T) {

	research := []string{
		"Compare how different databases resolve deadlocks",
		"Survey of automated patch generation techniques",
		"Research how open-source projects fix concurrency bugs",
		"Investigate how Go's GC fixes memory leaks (read-only survey)",
		"Study how teams apply patches in production",
		"Analyze the refactoring, fixes, and patches used",
	}
	for _, task := range research {
		if !detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = false, want true (research task wrongly vetoed)", task)
		}
	}

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
