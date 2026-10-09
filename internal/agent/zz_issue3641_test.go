package agent

// #3641 probes: the fallback hint pathway must fire for zero-match
// SUCCESS results of search-class tools (grep renders "No matches found"
// with IsError unset), while non-search tools and ordinary content must
// stay out.

import (
	"strings"
	"testing"
)

func TestIssue3641_ZeroMatchSuccessDetection(t *testing.T) {
	if !isZeroMatchSuccess("grep", "No matches found for pattern \"foo\" in /src") {
		t.Fatal("grep zero-match success must be detected")
	}
	if !isZeroMatchSuccess("code_search", "no results found for query") {
		t.Fatal("code_search zero-match success must be detected")
	}
	if isZeroMatchSuccess("read_file", "The file says: no match for X in this doc") {
		t.Fatal("non-search tool content must never trigger")
	}
	if isZeroMatchSuccess("grep", "42 matches:\nfoo.go: match at line 3") {
		t.Fatal("grep with real matches must not trigger")
	}
}

func TestIssue3641_HintTextForZeroMatch(t *testing.T) {
	// The existing detector branch must produce the broaden-pattern hint
	// for the exact zero-match success content.
	hint := toolFallbackHint("grep", "No matches found for pattern \"zzz\" in /src")
	if hint == "" || !strings.Contains(hint, "broaden") {
		t.Fatalf("zero-match grep must yield broaden hint, got %q", hint)
	}
}
