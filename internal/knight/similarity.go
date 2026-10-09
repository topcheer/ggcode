package knight

import (
	"regexp"
	"strings"
	"unicode"
)

// similarityTokenStopwords is a small English/code stopword list to reduce
// noise when comparing two short skill descriptions.
var similarityTokenStopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "or": {}, "the": {}, "of": {}, "to": {},
	"for": {}, "in": {}, "on": {}, "at": {}, "by": {}, "with": {}, "from": {},
	"is": {}, "are": {}, "be": {}, "this": {}, "that": {}, "it": {}, "its": {},
	"as": {}, "if": {}, "when": {}, "while": {}, "use": {}, "uses": {},
	"using": {}, "do": {}, "does": {}, "not": {}, "no": {}, "yes": {},
	"will": {}, "should": {}, "can": {}, "may": {}, "must": {},
	"step": {}, "steps": {}, "skill": {}, "skills": {},
}

var similarityTokenPattern = regexp.MustCompile(`[a-z0-9][a-z0-9_\-./]*`)

// similarityCJKStopwordRunes is a minimal Chinese function-word (虚词) set.
// Removed from CJK runs BEFORE bigram splitting so "数据库的备份" and
// "数据库备份" fingerprint identically (#3637).
var similarityCJKStopwordRunes = map[rune]struct{}{
	'的': {}, '与': {}, '和': {}, '及': {}, '在': {}, '对': {},
	'从': {}, '被': {}, '是': {}, '了': {}, '等': {},
}

// isWideRuneForSimilarity reports whether r belongs to the wide-script
// ranges handled by the run-based CJK tokenizer below.
func isWideRuneForSimilarity(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		(r >= 0xFF01 && r <= 0xFF5E)
}

// addCJKRunTokens splits one wide-script rune run into overlapping bigrams
// (run length >= 2) or a single-rune token (run length 1) after dropping
// Chinese function-word runes.
func addCJKRunTokens(tokens map[string]struct{}, run []rune) {
	filtered := run[:0:0]
	for _, r := range run {
		if _, ok := similarityCJKStopwordRunes[r]; ok {
			continue
		}
		filtered = append(filtered, r)
	}
	if len(filtered) == 0 {
		return
	}
	if len(filtered) == 1 {
		tokens[string(filtered[0])] = struct{}{}
		return
	}
	for i := 0; i+1 < len(filtered); i++ {
		tokens[string(filtered[i:i+2])] = struct{}{}
	}
}

// tokenizeForSimilarity normalizes a free-form string into a stable set of
// lowercased tokens suitable for set-based similarity comparisons.
func tokenizeForSimilarity(s string) map[string]struct{} {
	tokens := make(map[string]struct{})
	if strings.TrimSpace(s) == "" {
		return tokens
	}
	lower := strings.ToLower(s)
	matches := similarityTokenPattern.FindAllString(lower, -1)
	// #1580-A: the ASCII-only pattern dropped every CJK rune - a Chinese
	// skill body (this repo's primary generation language) tokenized to
	// EMPTY, fingerprint/jaccard scored 0, and A/B replay relevance was
	// silently useless. CJK runes become tokens.
	// #1622-A: the hand-written range covered ONLY Han U+4E00-9FFF -
	// Korean scored 0 tokens (jaccard 0, dedup dead), Japanese kana
	// U+3040-30FF dropped (fingerprints skewed), Ext-A/compat ideographs
	// missed. Use the unicode tables (Han/Hangul/Hiragana/Katakana).
	// #1622-B: full-width ASCII (U+FF01-FF5E, web/doc paste) was in NO
	// range - "版本Ｖ１．２" and "版本" both fingerprinted {版,本} and
	// scored 1.0 (distinct versions discarded as duplicates). Included.
	// #3637: per-rune single-character tokens systematically inflated
	// jaccard for same-domain same-prefix Chinese skill families
	// ("自动化数据库备份" vs "自动化数据库恢复" = 6/10 = 0.60 >= 0.6
	// threshold) and isKnownCandidate silently dropped the survivor via
	// queue.Remove. CJK runs are now split into overlapping BIGRAMS
	// (after dropping Chinese function-word runes) so a trailing
	// two-character difference materially moves the score:
	// 8-char names -> 7 bigrams each, inter 5 / union 9 = 0.556 < 0.6
	// (not a duplicate), while genuinely identical names still score 1.0.
	var run []rune
	flushRun := func() {
		if len(run) > 0 {
			addCJKRunTokens(tokens, run)
			run = run[:0]
		}
	}
	for _, r := range lower {
		if isWideRuneForSimilarity(r) {
			run = append(run, r)
			continue
		}
		flushRun()
	}
	flushRun()
	for _, m := range matches {
		m = strings.Trim(m, "._-/")
		if len(m) < 2 {
			continue
		}
		if _, ok := similarityTokenStopwords[m]; ok {
			continue
		}
		tokens[m] = struct{}{}
	}
	return tokens
}

// jaccardSimilarity returns the size of the intersection over the size of the
// union of two token sets, in the range [0, 1].
func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for tok := range a {
		if _, ok := b[tok]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// similarityBaseDuplicateThreshold is the jaccard score at which two English
// fingerprints are considered duplicates (pre-#3637 behavior, unchanged).
const similarityBaseDuplicateThreshold = 0.6

// similarityCJKDuplicateThreshold is the higher duplicate gate for
// CJK-dominant fingerprints (#3637). Bigram tokenization alone is NOT enough:
// the full fingerprint of the issue's pair (name + description) still scores
// 9/15 = 0.60 against the 0.6 gate, because the shared prefix dominates the
// union. Distinct same-family Chinese skills routinely cluster in the
// 0.55-0.70 band, while genuine duplicates (identical modulo function words)
// score 1.0, so the CJK gate is raised to 0.75.
const similarityCJKDuplicateThreshold = 0.75

// cjkDominantFingerprint reports whether fp contains any wide-script (CJK)
// token produced by the run-based tokenizer.
func cjkDominantFingerprint(fp map[string]struct{}) bool {
	for tok := range fp {
		for _, r := range tok {
			if isWideRuneForSimilarity(r) {
				return true
			}
		}
	}
	return false
}

// similarityDuplicateThreshold returns the jaccard score at which a candidate
// fingerprint counts as a duplicate of a known skill, per its script:
// 0.6 for ASCII fingerprints, 0.75 for CJK-dominant ones (#3637).
func similarityDuplicateThreshold(fp map[string]struct{}) float64 {
	if cjkDominantFingerprint(fp) {
		return similarityCJKDuplicateThreshold
	}
	return similarityBaseDuplicateThreshold
}

// skillSimilarityFingerprint extracts a token set from a skill's identifying
// fields (name + description + when_to_use heading text in body, if present).
func skillSimilarityFingerprint(name, description, body string) map[string]struct{} {
	parts := []string{name, description}
	if body != "" {
		parts = append(parts, extractWhenToUseSection(body))
	}
	return tokenizeForSimilarity(strings.Join(parts, " "))
}

var whenToUseHeading = regexp.MustCompile(`(?im)^##\s*when\s*to\s*use`)
var whenNotToUseHeading = regexp.MustCompile(`(?im)^##\s*when\s*not\s*to\s*use`)
var anyHeading = regexp.MustCompile(`(?m)^##\s+`)

// extractWhenToUseSection returns the body text of a ## When to Use section,
// or an empty string if no such section exists.
func extractWhenToUseSection(body string) string {
	loc := whenToUseHeading.FindStringIndex(body)
	if loc == nil {
		return ""
	}
	rest := body[loc[1]:]
	stop := anyHeading.FindStringIndex(rest)
	if loc2 := whenNotToUseHeading.FindStringIndex(rest); loc2 != nil {
		if stop == nil || loc2[0] < stop[0] {
			stop = loc2
		}
	}
	if stop == nil {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:stop[0]])
}
