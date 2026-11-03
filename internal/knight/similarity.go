package knight

import (
	"regexp"
	"strings"
	"unicode"
)

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

var similarityCJKStopwordRunes = map[rune]struct{}{
	'的': {}, '与': {}, '和': {}, '及': {}, '在': {}, '对': {},
	'从': {}, '被': {}, '是': {}, '了': {}, '等': {},
}

func isWideRuneForSimilarity(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		(r >= 0xFF01 && r <= 0xFF5E)
}

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

func tokenizeForSimilarity(s string) map[string]struct{} {
	tokens := make(map[string]struct{})
	if strings.TrimSpace(s) == "" {
		return tokens
	}
	lower := strings.ToLower(s)
	matches := similarityTokenPattern.FindAllString(lower, -1)

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

const similarityBaseDuplicateThreshold = 0.6

const similarityCJKDuplicateThreshold = 0.75

func cjkDominantFingerprint(fp map[string]struct{}) bool {
	if len(fp) == 0 {
		return false
	}
	cjk := 0
	for tok := range fp {
		for _, r := range tok {
			if isWideRuneForSimilarity(r) {
				cjk++
				break
			}
		}
	}
	return cjk*2 >= len(fp)
}

func similarityDuplicateThreshold(fp map[string]struct{}) float64 {
	if cjkDominantFingerprint(fp) {
		return similarityCJKDuplicateThreshold
	}
	return similarityBaseDuplicateThreshold
}

func duplicateThresholdForPair(a, b map[string]struct{}) float64 {
	ta := similarityDuplicateThreshold(a)
	tb := similarityDuplicateThreshold(b)
	if ta < tb {
		return ta
	}
	return tb
}

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
