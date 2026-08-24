package agent

import "testing"

// TestIssue1008NormalizeRuleCategory pins the fix: LLM-produced category
// variants are canonicalized at the AddRule boundary so the rule matches
// tools preventively instead of silently dead-ending (categoryMatchesTool
// default->false) and hiding from /rules.
func TestIssue1008NormalizeRuleCategory(t *testing.T) {
	cases := map[string]string{
		"build":       "build",
		"Build":       "build",
		"  test  ":    "test",
		"GIT":         "git",
		"lint":        "convention", // not in enum -> fallback
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
