package agent

import "testing"

// Regression tests for GitHub issue #1178.
//
// #1178: intent phrases were matched with bare strings.Contains, so "let me
// fix" also matched inside "let me fixate". The fabricated fix category,
// combined with a real verify category, produced 2+ distinct stated
// categories and flipped the #1162 multi-intent exemption on - silencing a
// genuine single-intent misalignment. The fix adds word-boundary matching
// (ASCII word bytes on both sides of the match, regexp \b equivalent).

// TestIssue1178_FixateDoesNotFabricateFixCategory drives the issue's exact
// scenario: "let me fixate" must not fabricate the fix category, so the
// single verify intent is NOT multi-intent and must escalate at window close
// instead of draining silently.
func TestIssue1178_FixateDoesNotFabricateFixCategory(t *testing.T) {
	s := newReasonActionState()
	text := "Let me fixate on the diff. I'll run the tests shortly."
	// Statement turn: fix action against a verify-only statement.
	if hint := s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}}); hint != "" {
		t.Fatalf("statement turn must not escalate: %s", hint)
	}
	// Drain the tolerance window; the verify intent must escalate because
	// the fake fix category no longer grants the multi-intent exemption.
	hint := ""
	for i := 0; hint == "" && i < raAlignmentWindow+2; i++ {
		hint = s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}})
	}
	if hint == "" {
		t.Fatal("'let me fixate' fabricated a fix category and silenced a genuine verify misalignment")
	}
	if s.warnings != 1 {
		t.Fatalf("expected exactly 1 warning, got %d", s.warnings)
	}
}

// TestIssue1178_ExtractIntentsWordBoundary pins the boundary semantics of
// the low-level classifier directly.
func TestIssue1178_ExtractIntentsWordBoundary(t *testing.T) {
	cases := []struct {
		text    string
		wantCat raCategory
		name    string
	}{
		{"Let me fixate on the diff.", raCatFix, "fixate must not yield fix"},
		{"I'll ready the config now.", raCatUnderstand, "ready must not yield read"},
		{"The results were underwhelming to test boundaries.", raCatVerify, "understated 'test' must not yield verify"},
		{"booklet me fix the bug.", raCatFix, "match starting mid-word must be rejected"},
	}
	for _, tc := range cases {
		got := extractRAIntents(tc.text)
		for _, c := range got {
			if c == tc.wantCat {
				t.Fatalf("%s: %q wrongly classified into category %v", tc.name, tc.text, c)
			}
		}
	}
}

// TestIssue1178_ExtractIntentsStillDetectsRealPhrases guards against the
// opposite regression: genuine phrases ending at real boundaries must keep
// working (trailing punctuation, whitespace, and end-of-text).
func TestIssue1178_ExtractIntentsStillDetectsRealPhrases(t *testing.T) {
	cases := []struct {
		text    string
		wantCat raCategory
		name    string
	}{
		{"Let me fix the bug.", raCatFix, "trailing punctuation"},
		{"I'll build the binary now", raCatVerify, "trailing whitespace"},
		{"I need to understand this.", raCatUnderstand, "phrase mid-text"},
		{"Let me verify", raCatVerify, "phrase at end of text"},
		{"请帮我: let me search。", raCatSearch, "CJK punctuation boundary"},
	}
	for _, tc := range cases {
		got := extractRAIntents(tc.text)
		found := false
		for _, c := range got {
			if c == tc.wantCat {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: %q lost detection of category %v, got %v", tc.name, tc.text, tc.wantCat, got)
		}
	}
}

// TestIssue1178_FindIntentPhraseConsistentWithExtract pins that the phrase
// reported in warnings uses the same boundary rule as classification (#1178):
// a category found by extractRAIntents must always yield its phrase.
func TestIssue1178_FindIntentPhraseConsistentWithExtract(t *testing.T) {
	text := "Let me fix the bug and verify."
	cats := extractRAIntents(text)
	if len(cats) == 0 {
		t.Fatal("expected at least one category")
	}
	for _, cat := range cats {
		if p := findRAIntentPhrase(text, cat); p == "" {
			t.Fatalf("category %v detected but findRAIntentPhrase returned empty phrase", cat)
		}
	}
	if p := findRAIntentPhrase("Let me fixate on this.", raCatFix); p != "" {
		t.Fatalf("mid-word match leaked into phrase lookup: %q", p)
	}
}
