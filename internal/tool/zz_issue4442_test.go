package tool

import (
	"strings"
	"testing"
)

// #4442: lenient occurrence recount for the line-numbers-stripped transform
// must (a) not strip digit+TAB prefixes from a file whose real lines start
// that way (TSV/log data) without a majority guard, and (b) count
// line-aligned rather than as a bare substring.

func TestIssue4442GuardedStripKeepsTSVContent(t *testing.T) {
	// Mixed file: normal code lines plus ONE line that happens to start
	// with digits+TAB. The majority guard must keep the file intact - the
	// old unguarded stripAllLineNumberPrefixes corrupted the lenient
	// recount by rewriting that line.
	mixed := "func A() {}\n123\tvalue\nfunc B() {}\nfunc C() {}\n"
	got := stripAllLineNumberPrefixesGuarded(mixed)
	if got != mixed {
		t.Fatalf("guarded strip altered a mostly-normal file:\n got: %q\nwant: %q", got, mixed)
	}

	// Reader-numbered paste: majority of lines carry the padded prefix, so
	// stripping applies.
	numbered := "func A() {}\n  10\tfunc B() {}\n  11\tfunc C() {}\n  12\t}\n"
	stripped := stripAllLineNumberPrefixesGuarded(numbered)
	if strings.Contains(stripped, "10\t") || strings.Contains(stripped, "11\t") {
		t.Fatalf("guarded strip failed to strip reader prefixes: %q", stripped)
	}
}

func TestIssue4442CountLineAligned(t *testing.T) {
	// Bug2: bare strings.Count would match "b\nc" inside "ab\ncd" mid-line.
	text := "ab\ncd\nac\n"
	if got := countLineAligned(text, "b\nc"); got != 0 {
		t.Fatalf("mid-line cross-break match counted: got %d, want 0", got)
	}
	if got := countLineAligned(text, "ab\ncd"); got != 1 {
		t.Fatalf("true line-aligned match missed: got %d, want 1", got)
	}
	// Overlapping/self-repeating: "a\na\na" contains "a\na" twice
	// non-overlapping.
	if got := countLineAligned("a\na\na", "a\na"); got != 1 {
		t.Fatalf("non-overlapping count wrong: got %d, want 1", got)
	}
	if got := countLineAligned("x", ""); got != 0 {
		t.Fatalf("empty probe should count 0, got %d", got)
	}
}
