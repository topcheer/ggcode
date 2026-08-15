package agent

import "testing"

// #424: a single conflict block in documentation/fixture content must not
// drag legitimate exact "=======" lines (setext headings, RST underlines)
// into the marker count — only separators INSIDE a conflict block count.
func TestConflictMarkersDocExampleNoGuiltByAssociation(t *testing.T) {
	content := "Intro text\n" +
		"<<<<<<< HEAD\n" +
		"ours\n" +
		"=======\n" +
		"theirs\n" +
		">>>>>>> branch\n" +
		"Setext Heading\n" +
		"=======\n" + // legitimate setext underline — outside any block
		"Another Heading\n" +
		"=======\n" // another legitimate underline
	w := checkMergeConflictMarkers("doc.md", content)
	if w == "" {
		t.Fatal("expected warning for real conflict block")
	}
	// Expect exactly 3 markers (start, one separator, end) — NOT 5.
	if !containsSubstr(w, "3 git merge conflict markers") {
		t.Errorf("expected count of 3 markers, got: %s", w)
	}
}

// #424: blank lines must not affect the growth ratio — the old all-lines
// count inflated the old-content denominator (hiding real duplication) and
// pushed new content over threshold.
func TestContentGrowthIgnoresBlankLines(t *testing.T) {
	// Old: 100 lines total, 10 non-empty. New: 10 non-empty lines all
	// duplicating old substance — all-line math made the ratio 0.1 (miss);
	// non-empty math gives 1.0 (no false positive either, but signal is now
	// based on substance).
	oldC := ""
	for i := 0; i < 10; i++ {
		oldC += "real code line\n"
		for j := 0; j < 9; j++ {
			oldC += "\n"
		}
	}
	newC := ""
	for i := 0; i < 10; i++ {
		newC += "real code line\n"
	}
	if w := checkContentGrowth("f.go", oldC, newC); w != "" {
		t.Errorf("no growth expected (same substance), got: %s", w)
	}

	// Real duplication: old has 10 substantive lines, new repeats them 5x
	// with blanks padding the old file.
	if w := checkContentGrowth("f.go", oldC, oldC+oldC+oldC+oldC+oldC); w == "" {
		t.Error("expected growth warning for 5x substantive duplication")
	}
}
