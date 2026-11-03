package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestConstraintExcerptTruncationRuneSafe(t *testing.T) {

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

	cjkPrefix := strings.Repeat("中", 6)
	text := cjkPrefix + "avoid touching auth module"

	ex := cvExtractExcerpt(text, 18, len("avoid"))
	if !utf8.ValidString(ex) {
		t.Errorf("violation excerpt invalid UTF-8: %q", ex)
	}

	long := strings.Repeat("好", 60)
	ex2 := cvExtractExcerpt(long, 0, len(long))
	if !utf8.ValidString(ex2) {
		t.Errorf("violation long excerpt invalid UTF-8: %q", ex2)
	}
	if got := len([]rune(strings.TrimSuffix(ex2, "..."))); got > cvExcerptLen {
		t.Errorf("excerpt runes %d exceed cap %d", got, cvExcerptLen)
	}
}
