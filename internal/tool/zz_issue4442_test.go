package tool

import (
	"strings"
	"testing"
)

// #4442 bug 1: a whole file whose REAL lines start with digits+TAB (TSV/log)
// must NOT have its prefixes stripped in the lenient recount - the majority
// guard keeps the content intact, so a unique edit stays unique.
func TestIssue4442_GuardedStripKeepsTSVContent(t *testing.T) {
	tsv := "101	hello world\n1337\tsecond row\n42\tthird row\n"
	if got := guardedStripAllLineNumberPrefixes(tsv); got != tsv {
		t.Fatalf("non-sequential TSV must pass through unchanged:\n got: %q\nwant: %q", got, tsv)
	}
	// Sequential 1..N is the reader's exact shape - still strips.
	reader := "  1\treal code line\n  2\tmore code\n  3\ttail\n"
	stripped := guardedStripAllLineNumberPrefixes(reader)
	if strings.Contains(stripped, "\treal code") {
		t.Fatalf("majority-vote reader output must still strip: %q", stripped)
	}
}

// #4442 bug 2: counting must be line-aligned - a cross-line probe matching
// MID-LINE (b\nc inside ab\ncd) must not count.
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

// End-to-end: lenientRecountRaw on the line-numbers-stripped path with a
// TSV-shaped file counts the real occurrence exactly once (bug 1 rejection
// path) and refuses mid-line inflation (bug 2).
func TestIssue4442_LenientRecountTSVUnique(t *testing.T) {
	content := "101\thello world\n1337\tsecond row\n42\tthird row\n7\thello world\n"
	// probe (with line-number transform context) for the block on rows 2..3 -
	// TSV digits prefixes must not turn this into a false multi.
	// probe as actually pasted from a reader view: reader numbers adorn the
	// TSV rows ("  2\t1337\tsecond row") - the :718 probe strip removes the
	// reader number, leaving the TSV content intact to match the (guard-
	// kept, unstripped) file.
	n, lines := lenientRecountRaw(content, "  2\t1337\tsecond row\n  3\t42\tthird row", "line-numbers-stripped")
	if n != 1 || len(lines) != 0 {
		t.Fatalf("unique block must recount as 1 occurrence (count-only branch returns no line list), got n=%d lines=%v", n, lines)
	}
}
