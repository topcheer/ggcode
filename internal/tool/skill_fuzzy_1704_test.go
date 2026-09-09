package tool

import "testing"

// #1704 case 3: exact matches outrank substring superstrings.
func TestSkillFuzzyExactOutranksSubstring1704(t *testing.T) {
	names := []string{"verify-changes", "verify", "verify-coverage", "verify-deep"}
	got := suggestSkills("verify", names)
	if len(got) == 0 {
		t.Fatal("no suggestions")
	}
	if got[0] != "verify" {
		t.Fatalf("exact match must rank first, got %q", got[0])
	}
}
