package tool

import (
	"strings"
	"testing"
)

func TestIssue4442_GuardedStripKeepsTSVContent(t *testing.T) {
	tsv := "101	hello world\n1337\tsecond row\n42\tthird row\n"
	if got := guardedStripAllLineNumberPrefixes(tsv); got != tsv {
		t.Fatalf("non-sequential TSV must pass through unchanged:\n got: %q\nwant: %q", got, tsv)
	}

	reader := "  1\treal code line\n  2\tmore code\n  3\ttail\n"
	stripped := guardedStripAllLineNumberPrefixes(reader)
	if strings.Contains(stripped, "\treal code") {
		t.Fatalf("majority-vote reader output must still strip: %q", stripped)
	}
}

func TestIssue4442_LineAlignedCount(t *testing.T) {
	file := "ab\ncd\n"
	if got := countLineAligned(file, "b\nc"); got != 0 {
		t.Fatalf("mid-line cross-line probe must not count, got %d", got)
	}
	if got := countLineAligned(file, "ab\ncd"); got != 1 {
		t.Fatalf("whole-line match must count, got %d", got)
	}
	if got := countLineAligned("x\nx\nx\n", "x"); got != 3 {
		t.Fatalf("single-line probe keeps plain count semantics, got %d", got)
	}
}

func TestIssue4442_LenientRecountTSVUnique(t *testing.T) {
	content := "101\thello world\n1337\tsecond row\n42\tthird row\n7\thello world\n"

	n, lines := lenientRecountRaw(content, "  2\t1337\tsecond row\n  3\t42\tthird row", "line-numbers-stripped")
	if n != 1 || len(lines) != 0 {
		t.Fatalf("unique block must recount as 1 occurrence (count-only branch returns no line list), got n=%d lines=%v", n, lines)
	}
}
