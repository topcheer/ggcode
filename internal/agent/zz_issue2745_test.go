package agent

// #2745 regression: classifyTaskType must match keywords on word
// boundaries - bare substring matching misclassified prompts whose words
// merely CONTAIN a keyword (laTEST, conTEST, ADDRESS, REBUILD,
// CHECKlist), polluting the persisted playbook fingerprint and system
// prompt injection downstream.

import "testing"

func TestIssue2745_ClassifyTaskTypeWordBoundary(t *testing.T) {
	cases := []struct {
		prompt, want string
	}{
		// The five issue scenarios: substring false-positives are gone.
		// (Category per current switch precedence - the fix removes the WRONG
		// hit; sentences with no whole keyword left classify "other".)
		{"Upgrade the dependency to the latest version", "other"}, // was test (la-test)
		{"Refactor the contest scoring module", "refactor"},       // was test (con-test)
		{"Create a checklist for onboarding", "feature"},          // was review (check)
		{"Address the failing build", "build"},                    // was feature (add-ress); whole-word "build" wins by precedence
		{"rebuild the parser", "other"},                           // was build (re-build); no whole keyword left
		// Boundary sanity: real keywords at word edges still classify.
		{"run the test suite", "test"},
		{"tests are failing", "bugfix"}, // "test" lacks a boundary inside "tests"; space-anchored " fail" hits bugfix
		{"fix the login bug", "bugfix"},
		{"build the project", "build"},
		{"deploy to prod", "build"},
		{"review this diff", "review"},
		{"check the logs", "review"},
		{"add a settings page", "feature"},
		{"create a new module", "feature"},
		{"refactor the store", "refactor"},
	}
	for _, tc := range cases {
		if got := classifyTaskType(tc.prompt); got != tc.want {
			t.Errorf("classifyTaskType(%q) = %q, want %q", tc.prompt, got, tc.want)
		}
	}
}

func TestIssue2745_SpaceAnchoredKeywordsKeepSubstringSemantics(t *testing.T) {
	// Keywords written with explicit spaces (" fail", "make ", "ci ",
	// "new ") encode their own anchoring and keep substring behavior.
	if got := classifyTaskType("the daemon will fail soon"); got != "bugfix" {
		t.Errorf("space-anchored ' fail' must still match bugfix, got %q", got)
	}
	if got := classifyTaskType("run make all"); got != "build" {
		t.Errorf("space-anchored 'make ' must still match build, got %q", got)
	}
	if got := classifyTaskType("new feature request"); got != "feature" {
		t.Errorf("space-anchored 'new ' must still match feature, got %q", got)
	}
}
