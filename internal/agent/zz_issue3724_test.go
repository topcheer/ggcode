package agent

// #3724 probe: extractQuoted paired CONTRACTION apostrophes (don't / it's /
// user's) as quote delimiters, turning the residue between two contractions
// into a phantom keyword ("Don't break the user's API contract" -> keyword
// "t break the user"). A prose-only plan item's ONLY keyword was that
// phantom, so checkPlanDrift judged it permanently unaddressed - a
// deterministic drift false-positive on prohibitive prose items.

import (
	"strings"
	"testing"
)

func TestIssue3724_ContractionApostropheNoPhantomKeyword(t *testing.T) {
	kw := extractKeywords("Don't break the user's API contract")
	for _, k := range kw {
		if strings.Contains(k, "t break the user") {
			t.Fatalf("contraction pairing produced a phantom keyword: %q", k)
		}
	}
	if len(kw) != 0 {
		t.Logf("expected no keywords for pure prose item, got: %v", kw)
	}
}

func TestIssue3724_ProsePlanItemNotPermanentlyUnaddressed(t *testing.T) {
	// Full path: a prohibitive prose item yields NO keywords, so it must not
	// be counted as unaddressed against a tool-evidence corpus. (Capitalized
	// words like "Make" are legitimately extracted by design - keep this
	// fixture all-lowercase to isolate the contraction path.)
	kw := extractKeywords("make sure it's backwards compatible and don't regress the API")
	if len(kw) != 0 {
		t.Fatalf("prose-only item must extract zero keywords, got: %v", kw)
	}
}

func TestIssue3724_RealQuotedTermStillExtracted(t *testing.T) {
	kw := extractKeywords("Update the 'config' struct and don't touch tests")
	found := false
	for _, k := range kw {
		if k == "config" {
			found = true
		}
		if strings.Contains(k, "t touch") || strings.Contains(k, "struct and don") {
			t.Fatalf("contraction residue leaked into keywords: %v", kw)
		}
	}
	if !found {
		t.Fatalf("real single-quoted term 'config' must still be extracted, got: %v", kw)
	}
}

func TestIssue3724_DoubleQuoteAndBacktickUnaffected(t *testing.T) {
	kw := extractKeywords(`Use "session manager" and the ` + "`proto`" + ` codec`)
	for _, want := range []string{"session manager", "proto"} {
		found := false
		for _, k := range kw {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("quoted term %q missing from %v", want, kw)
		}
	}
}
