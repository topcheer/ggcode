package agent

// #5793: scanScopeCreep built its excerpt windows with byte arithmetic
// (start-20 / loc[1]+40 / excerpt[:80]) with no rune-boundary handling -
// the #5775/#5776 family residue at its 4th site. In CJK-mixed agent
// output every window edge lands mid-rune, producing invalid UTF-8 that
// is injected into the provider stream via maybeWarnScopeCreep, and the
// corrupted byte strings also poison the L80 dedup key so one hit can be
// counted multiple times (observed 2 logical signals reported as 4).
// These tests pin the rune-safe form (same as premature_abstract #5776).

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const issue5793Sample = "顺便我也会先处理完这个问题因为since I'm already here顺手清理一下不需要的导入语句以及其他的小问题另外I cleaned up the imports也都处理好了"

func TestIssue5793ExcerptsRuneSafe(t *testing.T) {
	hits := scanScopeCreep(issue5793Sample)
	if len(hits) == 0 {
		t.Fatal("sample must trigger scope-creep patterns (sanity)")
	}
	for i, h := range hits {
		if !utf8.ValidString(h.excerpt) {
			t.Fatalf("hit %d (%s): excerpt is invalid UTF-8: %q", i, h.category, h.excerpt)
		}
	}
}

func TestIssue5793DedupKeyNotPoisoned(t *testing.T) {
	// The sample contains exactly TWO pattern phrases ("since I'm already
	// here" -> action #1, "I cleaned up the imports" -> action #4). The
	// byte-corrupted dedup key made the same hit count multiple times
	// (production capture: 2 signals reported as 4). Rune-safe excerpts
	// restore one hit per distinct phrase.
	hits := scanScopeCreep(issue5793Sample)
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want exactly 2 (one per distinct pattern phrase): %+v", len(hits), hits)
	}
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.excerpt] {
			t.Fatalf("duplicate excerpt reported twice: %q", h.excerpt)
		}
		seen[h.excerpt] = true
	}
}

func TestIssue5793ASCIIDetectionUnchanged(t *testing.T) {
	text := "Since I'm already here I'll also fix the config file. I cleaned up the imports as well."
	hits := scanScopeCreep(text)
	if len(hits) < 2 {
		t.Fatalf("ASCII detection regressed: got %d hits, want >= 2", len(hits))
	}
	for _, h := range hits {
		if !utf8.ValidString(h.excerpt) || !strings.Contains(h.excerpt, "here") && !strings.Contains(h.excerpt, "cleaned") {
			t.Fatalf("unexpected excerpt: %q", h.excerpt)
		}
	}
}
