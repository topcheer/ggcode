package agent

// #5793 residual battery (takeOwnership seat): the landed fix's own tests
// leave two dimensions unpinned, verified against the pre-fix parent:
//   1. TestIssue5793SecondCutCountsRunesNotBytes - the >80-RUNE truncation
//      branch (truncateRunesSafe second cut) is never exercised post-fix:
//      the sample's excerpts stay under 80 runes, so a regression back to
//      byte arithmetic in the length check / second cut would go unnoticed.
//   2. TestIssue5793WarningStreamSafe - the consumer path: maybeWarnScopeCreep
//      output goes straight into contextManager.Add(Role:"user") -> the
//      next-round provider prompt (chat_response_processing.go). Nothing
//      pinned that the injected warning itself is valid UTF-8 and reports
//      the true signal count (the production capture showed U+FFFD).
// Both tests fail at the pre-fix parent and pass at the fix.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 20 ASCII lead + 53-char phrase + 14 ASCII + 26 CJK: 113 runes (> 80) with
// byte-80 landing mid-CJK-rune under the old byte arithmetic.
const issue5793LongWindow = "aaaaaaaaaaaaaaaaaaaa" +
	"I spotted a few additional problems that I also fixed" +
	"bbbbbbbbbbbbbb一二三四五六七八九十甲乙丙丁戊己庚辛壬癸子丑寅卯辰巳午未申"

func TestIssue5793SecondCutCountsRunesNotBytes(t *testing.T) {
	hits := scanScopeCreep(issue5793LongWindow)
	if len(hits) == 0 {
		t.Fatal("long-window sample must trigger at least one pattern (sanity)")
	}
	found := false
	for i, h := range hits {
		if !utf8.ValidString(h.excerpt) {
			t.Fatalf("hit %d: excerpt is invalid UTF-8: %q", i, h.excerpt)
		}
		n := len([]rune(h.excerpt))
		if n > 80 {
			t.Fatalf("hit %d: excerpt has %d runes, want <= 80", i, n)
		}
		if strings.HasSuffix(h.excerpt, "...") {
			found = true
			if n != 80 {
				t.Fatalf("hit %d: truncated excerpt has %d runes, want exactly 80 (77 + ellipsis)", i, n)
			}
		}
	}
	if !found {
		t.Fatal("long-window sample must exercise the >80-rune truncation branch")
	}
}

func TestIssue5793WarningStreamSafe(t *testing.T) {
	a := &Agent{scopeCreep: newScopeCreepState()}
	w := a.maybeWarnScopeCreep(issue5793Sample)
	if w == "" {
		t.Fatal("sample must fire a scope-creep warning (sanity)")
	}
	if !utf8.ValidString(w) {
		t.Fatalf("warning injected into the provider stream is invalid UTF-8: %q", w)
	}
	if !strings.Contains(w, "2 signal(s)") {
		t.Fatalf("warning must report the true signal count for the sample, got: %.120s", w)
	}
	if !strings.Contains(w, "since I'm already here") || !strings.Contains(w, "I cleaned up the imports") {
		t.Fatalf("warning must quote both detected phrases: %q", w)
	}
}
