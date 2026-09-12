package agent

// #2125 regression, two detectors:
//   - normalizeIntegrityMsg normalized EVERY digit run, collapsing the
//     #605 G4 count suffixes ("(2 unclosed total)" == "(3 unclosed total)")
//     so only a 1→2 worsening surfaced; 2→3, 3→4 ... were silent.
//   - checkEditBlastRadius built line sets from UNtrimmed content while the
//     denominator used trimmed, so cleaning up trailing blank lines
//     produced "modified 13 of 20 lines (65%)" and tripped the gate.

import (
	"strings"
	"testing"
)

func TestNormalizeKeepsCountSuffixes(t *testing.T) {
	two := "line 10: unclosed '{' — missing closing delimiter (2 unclosed total)"
	three := "line 10: unclosed '{' — missing closing delimiter (3 unclosed total)"
	if normalizeIntegrityMsg(two) == normalizeIntegrityMsg(three) {
		t.Fatal("2→3 worsening must normalize DIFFERENTLY (#605 G4: worsening writes must surface)")
	}
	more2 := "unbalanced tags (and 2 more)"
	more3 := "unbalanced tags (and 3 more)"
	if normalizeIntegrityMsg(more2) == normalizeIntegrityMsg(more3) {
		t.Fatal("(and N more) count must survive normalization")
	}
}

func TestNormalizeStillCollapsesLineRefs(t *testing.T) {
	a := "line 10: unterminated string literal"
	b := "line 15: unterminated string literal"
	if normalizeIntegrityMsg(a) != normalizeIntegrityMsg(b) {
		t.Fatal("line-position shifts must not surface as new problems")
	}
	c := "'}' does not match '{' opened at line 10"
	d := "'}' does not match '{' opened at line 42"
	if normalizeIntegrityMsg(c) != normalizeIntegrityMsg(d) {
		t.Fatal("opened-at line refs must normalize")
	}
	// A count that is NOT a line ref keeps its digits (content counts).
	e := "missing closing quote followed by 3 '#'"
	f := "missing closing quote followed by 5 '#'"
	if normalizeIntegrityMsg(e) == normalizeIntegrityMsg(f) {
		t.Fatal("content counts (backtick run length) must survive normalization")
	}
}

func TestBlastRadiusIgnoresTrailingBlankCleanup(t *testing.T) {
	// 20 body lines + 13 trailing blanks; the edit only deletes the blanks.
	body := strings.Repeat("func line%d() {}\n", 20)
	old := body + strings.Repeat("\n", 13)
	if got := checkEditBlastRadius("f.go", old, body); got != "" {
		t.Fatalf("pure trailing-blank cleanup must not trip blast radius, got: %s", got)
	}
}

func TestBlastRadiusStillCatchesRealRewrites(t *testing.T) {
	old := strings.Repeat("original line %d\n", 30)
	rewrite := strings.Repeat("replacement line %d\n", 30)
	if got := checkEditBlastRadius("f.go", old, rewrite); got == "" {
		t.Fatal("a full rewrite must still trip blast radius")
	}
}
