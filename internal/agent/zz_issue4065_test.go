package agent

import "testing"

// #4065: fixIntentRe treats hyphens as word boundaries, so research tasks
// that merely MENTION a branch/PR identifier (fix-4012, patch-4013,
// resolve-4016 - this repo's own branch naming convention) fired the
// fix-intent override and disabled research mode, exposing read-only runs
// to the four stuck detectors' implementation-phase hints.
func TestDetectResearchModeHyphenatedBranchTokens(t *testing.T) {
	research := []string{
		"analyze branch fix-4012 divergence",
		"review the patch-4013 PR changes and assess impact",
		"investigate the resolve-4016 regression",
		"research the fix-4012 and fix-4013 branches",
		"survey the fix_4012 underscore variant",
		"analyze the fix-intent detection logic",
		"review patch-set generation approaches", // hyphenated compound led by verb
	}
	for _, task := range research {
		if !detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = false, want true (identifier mention, not fix intent)", task)
		}
	}
}

// #4065/#3990/#4044 control: genuine fix intent must still disable research
// mode, and the subordinate-clause false positives stay excluded.
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
	// #4044 regression: subordinate-clause verbs must not match the
	// fix-intent regex at all (checked directly, since these sentences
	// are research tasks and detectResearchMode itself returns true).
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
