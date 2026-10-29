package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #4076: trimFindingSnippet byte-sliced at researchSnippetCap, splitting
// CJK runes mid-sequence and injecting U+FFFD garbage into the research
// draft. The cut must back off to a rune boundary.
func TestIssue4076SnippetCutRespectsRunes(t *testing.T) {
	// CJK runes are 3 bytes: fill so the cap lands mid-rune.
	s := strings.Repeat("汉", researchSnippetCap/3+4)
	got := trimFindingSnippet(s)
	if !utf8.ValidString(got) {
		t.Fatalf("snippet contains invalid UTF-8 (split rune): %q", got)
	}
	if len(got) > researchSnippetCap {
		t.Fatalf("cap exceeded: %d > %d", len(got), researchSnippetCap)
	}
	// ASCII stays exact.
	ascii := strings.Repeat("a", researchSnippetCap+10)
	if len(trimFindingSnippet(ascii)) != researchSnippetCap {
		t.Fatal("ASCII cap must stay exact")
	}
}
