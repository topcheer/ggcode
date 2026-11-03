package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssue4076SnippetCutRespectsRunes(t *testing.T) {

	s := strings.Repeat("汉", researchSnippetCap/3+4)
	got := trimFindingSnippet(s)
	if !utf8.ValidString(got) {
		t.Fatalf("snippet contains invalid UTF-8 (split rune): %q", got)
	}
	if len(got) > researchSnippetCap {
		t.Fatalf("cap exceeded: %d > %d", len(got), researchSnippetCap)
	}

	ascii := strings.Repeat("a", researchSnippetCap+10)
	if len(trimFindingSnippet(ascii)) != researchSnippetCap {
		t.Fatal("ASCII cap must stay exact")
	}
}
