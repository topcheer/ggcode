package daemon

// #3462 probe: byte-cut truncation in summarizeToolResult /
// truncateForTerminal split multi-byte CJK runes and emitted invalid
// UTF-8 tails that terminals render as mojibake. Both must now cut at a
// rune boundary.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssue3462_TruncateRunesSafeBoundary(t *testing.T) {
	// 20 CJK runes = 60 bytes; cut targets land inside runes.
	s := strings.Repeat("中", 20)
	for _, cut := range []int{1, 2, 4, 58, 61} {
		got := truncateRunesSafe(s, cut)
		if !utf8.ValidString(got) {
			t.Fatalf("cut %d produced invalid UTF-8", cut)
		}
		if len(got) > cut {
			t.Fatalf("cut %d produced %d bytes", cut, len(got))
		}
	}
	if got := truncateRunesSafe(s, 60); got != s {
		t.Fatalf("in-boundary cut must be identity")
	}
}

func TestIssue3462_TruncateForTerminalCJK(t *testing.T) {
	// 40 CJK runes = 120 bytes; maxLen=100 lands mid-rune (byte 97).
	text := strings.Repeat("中", 40)
	got := truncateForTerminal(text, 100)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateForTerminal emitted invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("ellipsis suffix missing: %q", got)
	}
	// Body before the ellipsis must be whole runes.
	if n := len(got) - 3; n%3 != 0 {
		t.Fatalf("body byte length %d not a multiple of the CJK rune width", n)
	}
}

func TestIssue3462_SummarizeToolResultCJK(t *testing.T) {
	long := strings.Repeat("构建", 40) // 240 bytes
	got := summarizeToolResult(long, 60)
	if !utf8.ValidString(got) {
		t.Fatalf("summarizeToolResult emitted invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("ellipsis suffix missing: %q", got)
	}
	// Short input untouched.
	if got := summarizeToolResult("ok", 60); got != "ok" {
		t.Fatalf("short input must pass through, got %q", got)
	}
}
