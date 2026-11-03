//go:build goolm

package agent

import (
	"strings"
	"testing"
)

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
