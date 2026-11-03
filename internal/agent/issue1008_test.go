package agent

import "testing"

func TestIssue1008NormalizeRuleCategory(t *testing.T) {
	cases := map[string]string{
		"build":       "build",
		"Build":       "build",
		"  test  ":    "test",
		"GIT":         "git",
		"lint":        "convention",
		"ci":          "convention",
		"":            "convention",
		" build ":     "build",
		"Security":    "security",
		"conventionX": "convention",
	}
	for in, want := range cases {
		if got := normalizeRuleCategory(in); got != want {
			t.Errorf("normalizeRuleCategory(%q) = %q, want %q", in, got, want)
		}
	}
}
