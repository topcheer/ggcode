package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	cdMaxWarns = 2

	cdThreshold = 2

	cdIndicatorWindowTurns = 3
)

type cdIndicator struct {
	pattern string
	iter    int
}

type criteriaDriftState struct {
	mu             sync.Mutex
	indicators     []cdIndicator
	seenCategories map[string]bool
	warnCount      int
}

func newCriteriaDriftState() *criteriaDriftState {
	return &criteriaDriftState{seenCategories: make(map[string]bool)}
}

func (c *criteriaDriftState) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.indicators = nil
	c.seenCategories = make(map[string]bool)
	c.warnCount = 0
}

var criteriaDriftPatterns = map[string][]string{

	"narrowing": {
		"the requirement is really only",
		"narrowing the scope of the requirement",
		"the acceptance criteria can be limited to",
		"only the core requirement matters",
		"reducing the acceptance criteria",
		"the essential requirement is just",
		"trimming the requirement to",
	},

	"substitution": {
		"instead of the original requirement",
		"rather than the requested",
		"instead of what was requested",
		"a simpler solution than requested",
		"i've simplified the requirement to",
		"substituting the requirement with",
	},

	"reclassification": {
		"the requested is out of scope for",
		"that requirement falls outside the scope",
		"that requirement is out of scope",
		"deferring the requested requirement",
		"that part of the request is out of scope",
	},

	"partial_complete": {
		"good enough for the requirement",
		"sufficient for the stated requirement",
		"the requirement is mostly met",
		"this covers the required functionality",
		"meets the acceptance criteria enough",
	},
}

func (c *criteriaDriftState) recordAssistantText(text string, iter int) {

	iter++
	c.mu.Lock()
	defer c.mu.Unlock()

	lower := strings.ToLower(text)
	newIndicators := []string{}

	sentenceIndicators := make(map[string]map[string]bool)

	sentences := cdSplitSentences(lower)
	for _, sentence := range sentences {
		sentenceIndicators[sentence] = make(map[string]bool)
	}

	for cat, pats := range criteriaDriftPatterns {
		for _, pat := range pats {
			if strings.Contains(lower, pat) {

				matchedSentence := cdFindSentenceForPattern(lower, pat)
				if matchedSentence != "" {

					if sentenceIndicators[matchedSentence] == nil {
						sentenceIndicators[matchedSentence] = make(map[string]bool)
					}
					if sentenceIndicators[matchedSentence][cat] {
						debug.Log("agent", "Criteria drift indicator SKIPPED (dedupe) (category=%s, sentence=%q): %q at iteration %d", cat, matchedSentence, pat, iter)
						continue
					}
				}

				if !cdContainsStr(newIndicators, pat) {

					if cdIsAuthExempt(text, pat, cat) {
						debug.Log("agent", "Criteria drift indicator EXEMPT (auth context) (category=%s): %q at iteration %d", cat, pat, iter)
						continue
					}
					newIndicators = append(newIndicators, pat)
					debug.Log("agent", "Criteria drift indicator (category=%s): %q at iteration %d", cat, pat, iter)

					if matchedSentence != "" {
						sentenceIndicators[matchedSentence][cat] = true
					}
				}
			}
		}
	}

	for _, pat := range newIndicators {
		c.indicators = append(c.indicators, cdIndicator{pattern: pat, iter: iter})
	}

	for cat := range criteriaDriftPatterns {
		for _, ind := range newIndicators {
			for _, pat := range criteriaDriftPatterns[cat] {
				if ind == pat {
					c.seenCategories[cat] = true
					break
				}
			}
		}
	}
}

var cdAuthMarkers = []string{
	"as requested",
	"as you requested",
	"per your instruction",
	"user asked",
	"as you asked",
	"you asked me",
	"you instructed",
	"as instructed",
}

func cdIsSentenceBoundary(b byte) bool {
	return b == '.' || b == '!' || b == '?' || b == ';' || b == '\n'
}

func cdSentenceStartBefore(lower string, idx int) int {
	for i := idx; i > 0; i-- {
		if cdIsSentenceBoundary(lower[i-1]) {
			start := i
			for start < len(lower) && cdIsSentenceBoundary(lower[start]) {
				start++
			}
			for start < len(lower) && (lower[start] == ' ' || lower[start] == '\t' || lower[start] == '\r') {
				start++
			}
			return start
		}
	}
	return 0
}

func cdSentenceEndAfter(lower string, idx int, includeBoundary bool) int {
	for i := idx; i < len(lower); i++ {
		if cdIsSentenceBoundary(lower[i]) {
			if includeBoundary {
				return i + 1
			}
			return i
		}
	}
	return len(lower)
}

func cdHasAuthContext(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range cdAuthMarkers {

		markerIdx := 0
		for {
			idx := strings.Index(lower[markerIdx:], marker)
			if idx == -1 {
				break
			}
			idx += markerIdx

			sentenceStart := cdSentenceStartBefore(lower, idx)

			if idx > sentenceStart {

				negationCheckStart := idx - 30
				if negationCheckStart < sentenceStart {
					negationCheckStart = sentenceStart
				}
				prefix := lower[negationCheckStart:idx]

				prefixTrimmed := strings.TrimLeft(prefix, " \t\r\n")
				if prefixTrimmed == "not" || prefixTrimmed == "never" ||
					strings.HasPrefix(prefixTrimmed, "not ") ||
					strings.HasPrefix(prefixTrimmed, "never ") ||
					strings.Contains(prefix, " not ") ||
					strings.HasSuffix(prefix, "not ") ||
					strings.Contains(prefix, " never ") ||
					strings.HasSuffix(prefix, "never ") ||
					strings.Contains(prefix, "n't") {

					markerIdx = idx + len(marker)
					continue
				}
			}

			if idx+len(marker) < len(lower) {

				sentenceEnd := cdSentenceEndAfter(lower, idx, false)

				suffix := lower[idx+len(marker) : sentenceEnd]

				if strings.HasPrefix(suffix, " me about") || strings.HasPrefix(suffix, " about") {

					if strings.Contains(marker, "asked") {
						markerIdx = idx + len(marker)
						continue
					}

				}
				var boundedSuffix string
				if len(suffix) > 20 {
					boundedSuffix = suffix[:20]
				} else {
					boundedSuffix = suffix
				}
				if strings.Contains(boundedSuffix, "mentioned") || strings.Contains(boundedSuffix, "noted") {
					markerIdx = idx + len(marker)
					continue
				}
			}

			return true
		}
	}
	return false
}

func cdIsAuthExempt(text, pattern string, category string) bool {

	if category != "narrowing" && category != "reclassification" && category != "substitution" && category != "partial_complete" {
		return false
	}

	lower := strings.ToLower(text)
	patternLower := strings.ToLower(pattern)

	patternIdx := 0
	for {
		relativeIdx := strings.Index(lower[patternIdx:], patternLower)
		if relativeIdx == -1 {
			break
		}
		patternIdx += relativeIdx

		sentenceStart := cdSentenceStartBefore(lower, patternIdx)

		sentenceEnd := cdSentenceEndAfter(lower, patternIdx, true)

		sentence := lower[sentenceStart:sentenceEnd]

		relativePatternIdx := patternIdx - sentenceStart
		patternPrefix := sentence[:relativePatternIdx]

		if cdHasAuthContext(patternPrefix) {
			return true
		}

		patternIdx += len(patternLower)
	}

	return false
}

func (c *criteriaDriftState) maybeWarn(iter int) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.warnCount >= cdMaxWarns {
		return ""
	}

	fresh := c.indicators[:0]
	for _, ind := range c.indicators {
		if iter-ind.iter <= cdIndicatorWindowTurns {
			fresh = append(fresh, ind)
		}
	}
	c.indicators = fresh
	if len(c.indicators) == 0 {
		return ""
	}

	cats := map[string]bool{}
	for cat, pats := range criteriaDriftPatterns {
		for _, ind := range c.indicators {
			for _, pat := range pats {
				if ind.pattern == pat {
					cats[cat] = true
					break
				}
			}
		}
	}

	if len(c.indicators) < cdThreshold {
		return ""
	}

	c.warnCount++

	n := len(c.indicators)
	if n > 5 {
		n = 5
	}
	sampleParts := make([]string, 0, n)
	for _, ind := range c.indicators[:n] {
		sampleParts = append(sampleParts, ind.pattern)
	}
	sample := strings.Join(sampleParts, "; ")

	c.indicators = nil

	return `[Success Criteria Integrity] You have used language that redefines or relaxes ` +
		`the task's success criteria: "` + sample + `". ` +
		`This is a form of proxy gaming - optimizing for "what I can show as done" ` +
		`rather than "what was actually requested." ` +
		`Before claiming completion, verify your deliverables match the ORIGINAL request exactly. ` +
		`If requirements cannot be fully met: (1) state explicitly which parts are incomplete, ` +
		`(2) explain why, (3) never silently substitute easier alternatives or reclassify ` +
		`unmet requirements as "out of scope" without acknowledging the deviation to the user.`
}

func cdContains(slice []cdIndicator, s string) bool {
	for _, v := range slice {
		if v.pattern == s {
			return true
		}
	}
	return false
}

func cdContainsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func cdSplitSentences(text string) []string {
	var sentences []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '.' || text[i] == '!' || text[i] == '?' || text[i] == ';' || text[i] == '\n' {
			end := i + 1

			for end < len(text) && (text[end] == ' ' || text[end] == '\t' || text[end] == '\n' || text[end] == '\r') {
				end++
			}
			if end > start {
				sentences = append(sentences, text[start:end])
			}
			start = end
		}
	}
	if start < len(text) {
		sentences = append(sentences, text[start:])
	}
	return sentences
}

func cdFindSentenceForPattern(text, pattern string) string {
	idx := strings.Index(text, pattern)
	if idx == -1 {
		return ""
	}

	start := 0
	for i := idx; i >= 0; i-- {
		if i > 0 && (text[i-1] == '.' || text[i-1] == '!' || text[i-1] == '?' || text[i-1] == ';' || text[i-1] == '\n') {
			if i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
				start = i
				break
			}
		}
	}

	end := len(text)
	for i := idx; i < len(text); i++ {
		if text[i] == '.' || text[i] == '!' || text[i] == '?' || text[i] == ';' || text[i] == '\n' {
			end = i + 1
			break
		}
	}
	return text[start:end]
}
