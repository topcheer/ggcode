package agent

import "testing"

func TestIssue2745_ClassifyTaskTypeWordBoundary(t *testing.T) {
	cases := []struct {
		prompt, want string
	}{

		{"Upgrade the dependency to the latest version", "other"},
		{"Refactor the contest scoring module", "refactor"},
		{"Create a checklist for onboarding", "feature"},
		{"Address the failing build", "build"},
		{"rebuild the parser", "other"},

		{"run the test suite", "test"},
		{"tests are failing", "bugfix"},
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
