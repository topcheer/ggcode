package tool

import (
	"testing"
)

func TestSuggestSkills_ExactHyphenUnderscore(t *testing.T) {
	names := []string{"browser-automation", "debug", "verify-changes", "review-changes"}
	sugs := suggestSkills("browser_automation", names)
	if len(sugs) == 0 {
		t.Fatal("expected at least one suggestion for browser_automation")
	}
	found := false
	for _, s := range sugs {
		if s == "browser-automation" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'browser-automation' in suggestions, got %v", sugs)
	}
}

func TestSuggestSkills_SubstringMatch(t *testing.T) {
	names := []string{"verify-changes", "verify-lint", "review-changes", "debug"}
	sugs := suggestSkills("verify", names)
	if len(sugs) == 0 {
		t.Fatal("expected suggestions for 'verify'")
	}
	// Should prioritize verify-changes and verify-lint as substring matches
	hasVerify := false
	for _, s := range sugs {
		if s == "verify-changes" || s == "verify-lint" {
			hasVerify = true
		}
	}
	if !hasVerify {
		t.Errorf("expected verify-* in suggestions, got %v", sugs)
	}
}

func TestSuggestSkills_CaseInsensitive(t *testing.T) {
	names := []string{"debug", "simplify", "spec"}
	sugs := suggestSkills("Debug", names)
	if len(sugs) == 0 {
		t.Fatal("expected suggestion for 'Debug'")
	}
	if sugs[0] != "debug" {
		t.Errorf("expected 'debug', got %v", sugs)
	}
}

func TestSuggestSkills_NoMatch(t *testing.T) {
	names := []string{"debug", "simplify", "spec"}
	sugs := suggestSkills("completely-unrelated-xyz123", names)
	if sugs != nil {
		t.Errorf("expected nil for unrelated query, got %v", sugs)
	}
}

func TestSuggestSkills_EmptyQuery(t *testing.T) {
	names := []string{"debug", "simplify"}
	sugs := suggestSkills("", names)
	if sugs != nil {
		t.Errorf("expected nil for empty query, got %v", sugs)
	}
}

func TestSuggestSkills_EmptyNames(t *testing.T) {
	sugs := suggestSkills("debug", nil)
	if sugs != nil {
		t.Errorf("expected nil for empty names, got %v", sugs)
	}
}

func TestSuggestSkills_LimitResults(t *testing.T) {
	// Many close matches should be capped at maxFuzzyResults (3)
	names := []string{"verify-changes", "verify-lint", "verify-regression", "verify-build"}
	sugs := suggestSkills("verify", names)
	if len(sugs) > maxFuzzyResults {
		t.Errorf("expected at most %d suggestions, got %d", maxFuzzyResults, len(sugs))
	}
}

func TestNormalizeSkillName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"browser-automation", "browserautomation"},
		{"browser_automation", "browserautomation"},
		{"Browser-Automation", "browserautomation"},
		{"  debug  ", "debug"},
		{"spec.v2", "specv2"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeSkillName(tt.input)
		if got != tt.want {
			t.Errorf("normalizeSkillName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSkillNotFoundMsg_NoLister(t *testing.T) {
	st := SkillTool{}
	msg := st.skillNotFoundMsg("foo")
	if msg != `skill "foo" not found` {
		t.Errorf("unexpected message: %q", msg)
	}
}
