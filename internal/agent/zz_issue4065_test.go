package agent

import "testing"

func TestDetectResearchModeHyphenatedBranchTokens(t *testing.T) {
	research := []string{
		"analyze branch fix-4012 divergence",
		"review the patch-4013 PR changes and assess impact",
		"investigate the resolve-4016 regression",
		"research the fix-4012 and fix-4013 branches",
		"survey the fix_4012 underscore variant",
		"analyze the fix-intent detection logic",
		"review patch-set generation approaches",
	}
	for _, task := range research {
		if !detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = false, want true (identifier mention, not fix intent)", task)
		}
	}
}

func TestDetectResearchModeFixIntentRegression(t *testing.T) {
	fix := []string{
		"investigate why tests fail and fix it",
		"Please fix the handler",
		"now first go repair the parser",
		"find the bug, then patch the config",
		"implement the stub",
	}
	for _, task := range fix {
		if detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = true, want false (genuine fix intent)", task)
		}
	}

	notFix := []string{
		"survey of automated patch generation",
		"explore how databases resolve deadlocks",
		"assess the impact of the refactoring",
	}
	for _, task := range notFix {
		if fixIntentRe.MatchString(stripFixIntentTokens(task)) {
			t.Errorf("fixIntentRe matched subordinate-clause verb in %q", task)
		}
	}
}
