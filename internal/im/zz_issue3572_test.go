package im

// #3572 probe: stripLocalPartMention must match and consume in ONE
// coordinate system. The old code located matches in ToLower(text) but
// consumed bytes of the ORIGINAL text with the same indices - expanding
// case mappings like U+0130 (İ -> i̇, 2 runes / 3 bytes from 2) broke the
// alignment and mangled everything after the first such rune.

import "strings"
import "testing"

func TestIssue3572_ExpandingFoldNoMisalignment(t *testing.T) {
	// "İ" lowercases to 3 bytes from 2 - the old index confusion started
	// here. The mention after it must still be stripped, not shredded.
	text := "İstanbul @alice harika"
	got := stripLocalPartMention(text, "alice")
	if got != "İstanbul  harika" {
		t.Fatalf("expanding-fold text mangled: got %q", got)
	}
}

func TestIssue3572_CaseInsensitiveAndBoundaryKept(t *testing.T) {
	if got := stripLocalPartMention("hey @Alice hi", "alice"); got != "hey  hi" {
		t.Fatalf("case-insensitive match regressed: %q", got)
	}
	// Longer handle intact (#2749 rule, now byte-level).
	if got := stripLocalPartMention("hey @alicebot hi", "alice"); got != "hey @alicebot hi" {
		t.Fatalf("boundary rule regressed: %q", got)
	}
	// Non-ASCII byte right after the mention is NOT a continuation.
	if got := stripLocalPartMention("hey @aliceé hi", "alice"); got != "hey é hi" {
		t.Fatalf("non-ASCII boundary should strip: %q", got)
	}
}

func TestIssue3572_MultibyteBeforeAndAfter(t *testing.T) {
	text := "中文 @bob 讯息 İmotion @bob end"
	got := stripLocalPartMention(text, "bob")
	if strings.Contains(got, "@bob") {
		t.Fatalf("mentions after multibyte runs not stripped: %q", got)
	}
	// Both runs survive intact.
	for _, want := range []string{"中文", "讯息", "İmotion", "end"} {
		if !strings.Contains(got, want) {
			t.Fatalf("multibyte content lost: %q missing %q", got, want)
		}
	}
}
