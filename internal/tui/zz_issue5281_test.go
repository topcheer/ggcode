package tui

import (
	"strings"
	"testing"
)

// #5281: the inspectorText zh branch was missing the three /search keys
// (Go switch has no fallthrough to the outer default), so zh users saw
// raw key names; meanwhile the en (default) branch carried Chinese copy
// for those keys, and search_results was a dead key with zero callers.

func TestIssue5281_ZhBranchHasSearchKeys(t *testing.T) {
	for _, key := range []string{"search_no_query", "search_no_results"} {
		got := inspectorText(LangZhCN, key)
		if got == key {
			t.Errorf("zh branch missing %q - user would see raw key name", key)
		}
		if isASCIIOnlyIssue5281(got) {
			t.Errorf("zh branch %q returned ASCII-only text (expected Chinese): %s", key, got)
		}
	}
}

func TestIssue5281_EnBranchIsEnglish(t *testing.T) {
	for _, key := range []string{"search_no_query", "search_no_results"} {
		got := inspectorText(LangEnglish, key)
		if got == key {
			t.Errorf("en branch missing %q", key)
		}
		if !isASCIIOnlyIssue5281(got) {
			t.Errorf("en branch %q returned non-English text (was Chinese before #5281): %s", key, got)
		}
	}
}

func TestIssue5281_DeadKeyRemoved(t *testing.T) {
	if got := inspectorText(LangEnglish, "search_results"); got != "search_results" {
		t.Errorf("dead key search_results should no longer resolve (got %q)", got)
	}
}

func TestIssue5281_LangEnConstantExists(t *testing.T) {
	// guard: LangEn is the language constant used by the outer default branch;
	// if the constant set changed, these pins must be updated too.
	_ = LangZhCN
	_ = LangEnglish
	if LangEnglish == LangZhCN {
		t.Fatal("LangEn must differ from LangZhCN for these pins to be meaningful")
	}
}

// rejects CJK ranges (Chinese text leaking into the en branch) but allows
// legitimate punctuation like em-dash.
func isASCIIOnlyIssue5281(s string) bool {
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) || (r >= 0xF900 && r <= 0xFAFF) {
			return false
		}
	}
	return true
}

var _ = strings.Contains // keep strings import if pins evolve
