package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

// DuplicateCheck checks whether a new memory entry would be a duplicate or
// near-duplicate of an existing one. This prevents the agent from creating
// redundant memories that waste context budget.
type DuplicateCheck struct {
	SimilarTo       string  // key of the most similar existing entry (if any)
	Similarity      float64 // 0-1 similarity score (1 = identical key)
	ExistingContent string  // content of the matched existing entry (truncated)
}

// IsDuplicate returns true if the similarity score is high enough to consider
// the new entry a duplicate.
func (dc DuplicateCheck) IsDuplicate() bool {
	return dc.Similarity >= 0.6
}

// CheckDuplicate inspects existing memories for potential duplicates of a
// new entry being saved. It uses token-set similarity (Jaccard) on the keys
// and a content overlap check.
//
// Returns a DuplicateCheck with the most similar existing entry, or an empty
// one if no existing entry is similar enough.
func (am *AutoMemory) CheckDuplicate(key, content string) DuplicateCheck {
	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "duplicate check: failed to read dir %s: %v", am.dir, err)
		return DuplicateCheck{}
	}

	newTokens := tokenize(key)
	if len(newTokens) == 0 {
		return DuplicateCheck{}
	}

	var best DuplicateCheck

	for _, m := range metas {
		existingTokens := tokenize(m.Key)
		sim := jaccardSimilarity(newTokens, existingTokens)

		if sim > best.Similarity {
			best = DuplicateCheck{
				SimilarTo:       m.Key,
				Similarity:      sim,
				ExistingContent: readExistingSnippet(am.dir, m.Key),
			}
		}

		// Also check if keys are identical (exact match). m.Key is the DISK
		// filename (disambiguateKey output), so compare against the same
		// derivation - the old sanitizeKey(key) never matched keys that got
		// a hash suffix (CJK/spaces/dots), so saving such a key twice never
		// reported the 1.0 duplicate it was (#2520, same fix shape as the
		// contradiction_check self-update comparison #1280).
		if m.Key == disambiguateKey(key, sanitizeKey(key)) {
			// #3049-C3: read THIS entry's content - the old code reused
			// best.ExistingContent, which belongs to whichever DIFFERENT entry
			// (e.g. "cmd-build" when saving "build-cmd") last won the
			// similarity loop (1.0 > 1.0 never replaces an earlier 1.0 twin).
			return DuplicateCheck{
				SimilarTo:       m.Key,
				Similarity:      1.0,
				ExistingContent: readExistingSnippet(am.dir, m.Key),
			}
		}
	}

	return best
}

// tokenize splits a string into lowercase alphanumeric tokens for comparison.
func tokenize(s string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var current strings.Builder

	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToLower(r))
		} else {
			if current.Len() > 0 {
				tokens[current.String()] = struct{}{}
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens[current.String()] = struct{}{}
	}
	return tokens
}

// jaccardSimilarity computes the Jaccard similarity coefficient between two
// token sets: |intersection| / |union|. Returns 0 if both are empty.
func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	intersection := 0
	for token := range a {
		if _, ok := b[token]; ok {
			intersection++
		}
	}

	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// FormatDuplicateWarning returns a human-readable warning message when a
// duplicate is detected.
func (dc DuplicateCheck) FormatDuplicateWarning(newKey string) string {
	if !dc.IsDuplicate() {
		return ""
	}
	if dc.Similarity >= 1.0 {
		return fmt.Sprintf("Warning: memory %q already exists. Consider updating the existing entry instead of creating a duplicate.", dc.SimilarTo)
	}
	return fmt.Sprintf("Warning: memory %q is very similar to existing %q (similarity: %.0f%%). Consider updating the existing entry or using a more distinct key.",
		newKey, dc.SimilarTo, dc.Similarity*100)
}

// readExistingSnippet loads the first 200 runes of an existing memory
// entry (#3049): rune-safe truncation (the old byte cut split CJK runes
// mid-sequence) and the exact-match path reads its OWN key's file instead
// of a similarity-loop leftover.
func readExistingSnippet(dir, key string) string {
	data, err := os.ReadFile(filepath.Join(dir, key+".md"))
	if err != nil {
		return ""
	}
	s := string(data)
	if len([]rune(s)) > 200 {
		return string([]rune(s)[:200]) + "..."
	}
	return s
}
