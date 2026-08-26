package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Regression guard for #1029: byte-sliced truncation of constraint excerpts
// could split a multi-byte rune and inject invalid UTF-8 into the reminder /
// warning text shown to the agent and terminal. Same family as #934.
func TestConstraintExcerptTruncationRuneSafe(t *testing.T) {
	// Build a mixed English+Chinese text long enough to exceed
	// constraintExcerptLen (120 bytes) with the byte cut landing mid-rune.
	mixed := "don't ! modify auth " + strings.Repeat("认证模块相关文件不能改", 20)

	excerpts := extractConstraints(mixed)
	if len(excerpts) == 0 {
		t.Fatal("expected at least one constraint excerpt from mixed text")
	}
	for _, e := range excerpts {
		if !utf8.ValidString(e) {
			t.Errorf("amnesia excerpt invalid UTF-8: %q", e)
		}
	}

	// cvExtractExcerpt: pos-10 back-slice must not split a rune either.
	cjkPrefix := strings.Repeat("中", 6) // 18 bytes, pos lands after them
	text := cjkPrefix + "avoid touching auth module"
	// pos points at 'a' of avoid (byte 18); pos-10 = 8 is mid-rune (bytes 6-8
	// are inside the 3rd CJK char). The fix must back up to byte 6.
	ex := cvExtractExcerpt(text, 18, len("avoid"))
	if !utf8.ValidString(ex) {
		t.Errorf("violation excerpt invalid UTF-8: %q", ex)
	}
	// And the > cvExcerptLen truncation branch: rune-safe.
	long := strings.Repeat("好", 60) // 180 bytes > 100
	ex2 := cvExtractExcerpt(long, 0, len(long))
	if !utf8.ValidString(ex2) {
		t.Errorf("violation long excerpt invalid UTF-8: %q", ex2)
	}
	if got := len([]rune(strings.TrimSuffix(ex2, "..."))); got > cvExcerptLen {
		t.Errorf("excerpt runes %d exceed cap %d", got, cvExcerptLen)
	}
}
