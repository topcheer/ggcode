package knight

import (
	"regexp"
	"strings"
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

// tokenizeForSimilarity normalizes a free-form string into a stable set of
// lowercased tokens suitable for set-based similarity comparisons.
func tokenizeForSimilarity(s string) map[string]struct{} {
	tokens := make(map[string]struct{})
	if strings.TrimSpace(s) == "" {
		return tokens
	}
	matches := similarityTokenPattern.FindAllString(strings.ToLower(s), -1)
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
