package agent

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
