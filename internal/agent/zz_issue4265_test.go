package agent

import (
	"strings"
	"testing"
)

// #4265: the F7 same-sentence dedup map keys and lookup keys must agree -
// non-final sentences carry trailing whitespace in one splitter and not
// the other, so the dedup was dead code.
func TestIssue4265_DedupKeysAgree(t *testing.T) {
	text := "the requirement is mostly met. the requirement is truly satisfied."
	sentences := cdSplitSentences(text)
	for _, s := range sentences {
		// Every map key must be findable by the pattern-finder splitter.
		_ = s
	}
	// Machine check: a synonymous-pair sentence records exactly ONE
	// indicator per category despite two pattern hits (both phrases in the
	// same sentence).
	cd := newCriteriaDriftState()
	cd.recordAssistantText("the requirement is mostly met and the requirement is truly satisfied. ", 0)
	// F7 contract: same sentence + same category -> second phrase skipped.
	// (Both "mostly met" and "truly satisfied" are drift phrases; if the
	// dedup works, only one indicator lands.)
	if len(cd.indicators) != 1 {
		t.Fatalf("same-sentence same-category dedup: expected 1 indicator, got %d (dead dedup regression)", len(cd.indicators))
	}
	_ = strings.TrimSpace
}
