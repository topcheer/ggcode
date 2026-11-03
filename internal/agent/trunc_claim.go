package agent

import (
	"regexp"
	"strings"
)

const (
	truncClaimMaxWarnings  = 2
	truncClaimMaxEvents    = 8
	truncClaimExcerptLimit = 120
)

type truncationEvent struct {
	toolName  string
	iteration int
}

type truncClaimState struct {
	events       []truncationEvent
	warnings     int
	lastWarnIter int
}

func newTruncClaimState() *truncClaimState {
	return &truncClaimState{lastWarnIter: -1}
}

func (s *truncClaimState) reset() {
	s.events = nil
	s.warnings = 0
	s.lastWarnIter = -1
}

func (s *truncClaimState) recordTruncation(toolName string, iteration int) {
	if len(s.events) >= truncClaimMaxEvents {
		s.events = s.events[1:]
	}
	s.events = append(s.events, truncationEvent{
		toolName:  toolName,
		iteration: iteration,
	})
}

func (s *truncClaimState) hasRecentTruncation(iteration int) (truncationEvent, bool) {
	for i := len(s.events) - 1; i >= 0; i-- {
		ev := s.events[i]

		if ev.iteration == iteration || ev.iteration == iteration-1 {
			return ev, true
		}
	}
	return truncationEvent{}, false
}

var completenessClaimPatterns = []*regexp.Regexp{

	regexp.MustCompile(`(?i)\b(?:only|exactly|just|precisely)\s+\d+\s+(?:file|match|result|occurrence|reference|instance)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b\d+\s+(?:file|match|result|occurrence|reference|instance)(?:es|s)?\s+(?:in total|total|altogether|found)\b`),
	regexp.MustCompile(`(?i)\btotal\s+of\s+\d+\s+(?:file|match|result|occurrence|reference|instance)(?:es|s)?\b`),

	regexp.MustCompile(`(?i)\b(?:these|the|those)\s+(?:are\s+)?(?:all|the only|every)\s+(?:the\s+)?(?:file|match|result|occurrence|reference|instance|location)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b(?:that'?s|this is|it is|those are|these are)\s+(?:all|everything)(?:\s+we)?\s+(?:found|see|have)?\b`),
	regexp.MustCompile(`(?i)\b(?:all|every|each)\s+(?:occurrence|instance|reference|match|location)(?:es|s)?\s+(?:of|to)\b`),

	regexp.MustCompile(`(?i)\bno\s+(?:other|more|further|additional)\s+(?:file|match|result|occurrence|reference|instance|location)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b(?:nothing|no)\s+else\s+(?:match|found|exist|appear)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\bthere\s+(?:are|is)\s+no\s+(?:other|more|further)\s+(?:file|match|result|reference|occurrence|instance|location)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b(?:couldn'?t|can'?t|did not|didn'?t)\s+find\s+(?:any\s+)?(?:other|more|further)\s+(?:file|match|result|reference|occurrence|instance)(?:es|s)?\b`),

	regexp.MustCompile(`(?i)\bfound\s+(?:all|every)\s+(?:the\s+)?(?:match|result|occurrence|reference|instance|location)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b(?:search|grep|scan)\s+(?:found|returned|revealed)\s+(?:all|every)\b`),

	regexp.MustCompile(`(?i)\b(?:here'?s|here is)\s+(?:the\s+)?(?:complete|full|comprehensive|exhaustive)\s+(?:list|set|overview|summary)s?\b`),
}

func truncationAcknowledged(text string) bool {
	lower := strings.ToLower(text)
	ackPhrases := []string{
		"truncat",
		"output was cut",
		"results may be incomplete",
		"may be missing",
		"not exhaustive",
		"partial results",
		"some results may be missing",
		"need to check if there are more",
		"there might be more",
		"could be more results",
	}
	for _, p := range ackPhrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func (s *truncClaimState) maybeWarnTruncClaim(text string, iteration int) string {
	if s.warnings >= truncClaimMaxWarnings {
		return ""
	}
	if len(text) == 0 {
		return ""
	}

	if s.lastWarnIter == iteration {
		return ""
	}

	ev, ok := s.hasRecentTruncation(iteration)
	if !ok {
		return ""
	}

	if truncationAcknowledged(text) {
		return ""
	}

	claims := 0
	var firstMatch string
	for _, p := range completenessClaimPatterns {
		loc := p.FindStringIndex(text)
		if loc != nil {
			claims++
			if firstMatch == "" {
				end := loc[1]
				if end-loc[0] > truncClaimExcerptLimit {
					end = loc[0] + truncClaimExcerptLimit
				}
				firstMatch = text[loc[0]:end]
			}
		}
		if claims >= 2 {
			break
		}
	}

	if claims == 0 {
		return ""
	}

	s.warnings++
	s.lastWarnIter = iteration

	toolLabel := ev.toolName
	if toolLabel == "" {
		toolLabel = "a previous tool"
	}

	return "[Truncated Output Completeness Check] The output from " + toolLabel +
		" was truncated in a recent call, but your response makes a completeness/exhaustiveness claim (\"" +
		strings.TrimSpace(firstMatch) + "...\". " +
		"The truncation means some results may have been cut off. " +
		"Before asserting exhaustive findings: (1) re-run the search with narrower scope " +
		"or filters to get complete results, (2) use offset/pagination to check for additional matches, " +
		"(3) explicitly acknowledge if results may be incomplete. " +
		"Do not claim \"all\", \"only N\", or \"no other\" unless you've verified the full dataset."
}
