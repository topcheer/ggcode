//go:build goolm

package agent

import (
	"strings"
	"testing"
)

// #4510: the negate object arm excluded '.', so "don't touch config.yaml"
// truncated to "...config" and the dotted filename never became an anchor
// (fail-open for the most common refusal shape). The arm now allows '.' and
// trimExcerptAtSentence cuts the greedy match at the next sentence boundary.
func TestIssue4510_DottedFilenameBecomesAnchor(t *testing.T) {
	text := "don't touch config.yaml during the cleanup"
	excerpts := refusalNegatePattern.FindAllString(text, -1)
	found := false
	for _, e := range excerpts {
		if !strings.Contains(e, "config.yaml") {
			continue
		}
		found = true
		anchors := refusalPathPattern.FindAllString(e, -1)
		ok := false
		for _, a := range anchors {
			if a == "config.yaml" {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("excerpt %q carries config.yaml but no path anchor extracted: %v", e, anchors)
		}
	}
	if !found {
		t.Fatalf("no excerpt carried the dotted filename: %v", excerpts)
	}

	// Sentence periods still terminate the match: a multi-sentence line
	// yields one candidate per negation sentence (old behavior preserved).
	multi := "don't touch internal/auth/anything.go. Only use pattern Y. Never run git push --force."
	seen := map[string]bool{}
	for _, e := range refusalNegatePattern.FindAllString(multi, -1) {
		seen[strings.TrimSpace(e)] = true
	}
	if !seenAnyContaining(seen, "internal/auth/anything.go") || !seenAnyContaining(seen, "--force") {
		t.Fatalf("multi-sentence line lost per-sentence candidates: %v", seen)
	}
}

func seenAnyContaining(set map[string]bool, sub string) bool {
	for k := range set {
		if strings.Contains(k, sub) {
			return true
		}
	}
	return false
}
