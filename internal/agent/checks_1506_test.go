package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #1506 case A pin: CJK rewording survives - bigram similarity instead of
// space-token Jaccard.
func Test1506CJKRewordNotDrop(t *testing.T) {
	if sim := todoDropSimilarityOf("修复登录bug", "修复登录的bug"); sim < todoDropSimilarity {
		t.Fatalf("CJK reword similarity %.2f must clear the threshold %.2f", sim, todoDropSimilarity)
	}
	// English path unchanged.
	if sim := todoDropSimilarityOf("write unit tests for parser", "write unit tests for parser"); sim != 1.0 {
		t.Fatalf("identical English items must be 1.0, got %.2f", sim)
	}
}

// #1506 case A pin: truncateStr never splits a rune.
func Test1506TruncateRuneSafe(t *testing.T) {
	out := truncateStr("修复登录页面的权限校验并补充单元测试用例", 8)
	if !utf8.ValidString(out) {
		t.Fatalf("truncateStr produced invalid UTF-8: %q", out)
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("truncated string should end with ..., got %q", out)
	}
}
