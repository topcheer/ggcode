package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	spiralMaxWarnings = 1

	spiralMinGap = 2

	spiralMinTopicLen = 4

	spiralMaxTopics = 15

	spiralMinCommittedTurns = 2
)

var spiralUncertaintyRe = regexp.MustCompile(`(?i)(?:I assume|assuming|I'm assuming|presumably|I think|probably|likely|my guess|if I had to guess|it seems like|appears to be|I believe this|without knowing|best guess|might be|could be)\b`)

var spiralCommittedRe = regexp.MustCompile(`(?i)(?:since we(?:'re| are)|because (?:the|this|we)|given that|now that|as (?:we|the)|so (?:the|this|we)|therefore|this means|which means|the \w+ (?:is|was|has|uses|requires|needs))\b`)

var spiralVerificationRe = regexp.MustCompile(`(?i)(?:i (?:have |'ve )?(?:verified|confirmed|validated)\b|i ran (?:the )?(?:test|build|lint|check)s?\b)`)

var spiralExecutionTools = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"write_command_input": true,
	"stop_command":        true,
	"browser":             true,
	"desktop_control":     true,
	"mobile_device":       true,
}

var spiralEffectGatedTools = map[string]bool{
	"browser":         true,
	"desktop_control": true,
}

const effectConfirmedMarker = "effect: confirmed"

func (a *Agent) recordSpiralVerification(toolName, resultContent string) {
	if a == nil || a.spiralState == nil {
		return
	}
	if !spiralExecutionTools[toolName] {
		return
	}
	if spiralEffectGatedTools[toolName] && !strings.Contains(resultContent, effectConfirmedMarker) {
		return
	}
	for i := range a.spiralState.topics {
		a.spiralState.topics[i].verified = true
	}
}

var spiralTopicWordRe = regexp.MustCompile(`\b([a-z][a-z0-9_-]{3,30})\b`)

var spiralStopWords = map[string]bool{
	"that": true, "this": true, "with": true, "from": true, "have": true,
	"will": true, "been": true, "they": true, "them": true, "what": true,
	"when": true, "were": true, "more": true, "some": true, "than": true,
	"then": true, "into": true, "only": true, "your": true, "which": true,
	"their": true, "would": true, "could": true, "should": true, "about": true,
	"there": true, "where": true, "after": true, "before": true, "being": true,
	"these": true, "those": true, "under": true, "still": true,
	"make": true, "made": true, "just": true, "also": true, "need": true,
	"here": true, "very": true, "like": true, "even": true, "most": true,
	"many": true, "much": true, "such": true, "same": true, "other": true,
	"first": true, "last": true, "next": true, "over": true, "does": true,
	"done": true, "case": true, "each": true, "both": true, "want": true,
	"look": true, "came": true, "back": true, "way": true, "part": true,
	"give": true, "once": true, "upon": true, "help": true, "work": true,
	"going": true, "left": true, "used": true, "uses": true,
	"probably": true, "might": true, "think": true, "guess": true,
	"assume": true, "assuming": true, "seems": true, "appears": true,
	"without": true, "knowing": true, "likely": true, "presumably": true,
	"since": true, "because": true, "given": true, "therefore": true,
}

type spiralTrackedTopic struct {
	word       string
	sourceTurn int
	verified   bool
}

type spiralHallucinationState struct {
	turn            int
	warnings        int
	topics          []spiralTrackedTopic
	committedCounts map[string]int
}

func newSpiralHallucinationState() *spiralHallucinationState {
	return &spiralHallucinationState{
		committedCounts: make(map[string]int),
	}
}

func (s *spiralHallucinationState) topicsAreVerified() bool {
	if len(s.topics) == 0 {
		return false
	}
	for i := range s.topics {
		if !s.topics[i].verified {
			return false
		}
	}
	return true
}

func (s *spiralHallucinationState) reset() {
	s.turn = 0
	s.warnings = 0
	s.topics = nil
	s.committedCounts = make(map[string]int)
}

func extractSpiralTopics(text string) []string {
	var topics []string
	seen := make(map[string]bool)

	locs := spiralUncertaintyRe.FindAllStringIndex(text, -1)
	for _, loc := range locs {

		windowStart := loc[1]
		windowEnd := windowStart + 200
		if windowEnd > len(text) {
			windowEnd = len(text)
		}
		window := strings.ToLower(text[windowStart:windowEnd])

		words := spiralTopicWordRe.FindAllString(window, -1)
		for _, w := range words {
			if len(w) < spiralMinTopicLen {
				continue
			}
			if spiralStopWords[w] {
				continue
			}
			if seen[w] {
				continue
			}
			seen[w] = true
			topics = append(topics, w)
			if len(topics) >= spiralMaxTopics {
				return topics
			}
		}
	}
	return topics
}

func detectCommittedTopics(text string, tracked []spiralTrackedTopic, curTurn int) []string {
	if len(tracked) == 0 {
		return nil
	}

	lowerText := strings.ToLower(text)
	var matched []string
	seen := make(map[string]bool)

	hasCommitted := spiralCommittedRe.MatchString(lowerText)
	if !hasCommitted {
		return nil
	}

	for _, t := range tracked {
		if t.verified {
			continue
		}
		if curTurn-t.sourceTurn < spiralMinGap {
			continue
		}
		if seen[t.word] {
			continue
		}
		if strings.Contains(lowerText, t.word) {

			if spiralUncertaintyRe.MatchString(lowerText) {

				stillHedging := false
				locs := spiralUncertaintyRe.FindAllStringIndex(lowerText, -1)
				for _, loc := range locs {
					windowEnd := loc[1] + 100
					if windowEnd > len(lowerText) {
						windowEnd = len(lowerText)
					}
					window := lowerText[loc[1]:windowEnd]
					if strings.Contains(window, t.word) {
						stillHedging = true
						break
					}
				}
				if stillHedging {
					continue
				}
			}
			seen[t.word] = true
			matched = append(matched, t.word)
		}
	}
	return matched
}

func (a *Agent) recordSpiralTurn(text string) {
	if a.spiralState == nil {
		return
	}
	s := a.spiralState
	s.turn++

	if spiralVerificationRe.MatchString(text) {
		for i := range s.topics {
			s.topics[i].verified = true
		}
	}

	newTopics := extractSpiralTopics(text)
	if len(newTopics) > 0 {
		for _, t := range newTopics {
			s.topics = append(s.topics, spiralTrackedTopic{
				word:       t,
				sourceTurn: s.turn,
			})
		}

	}

	if len(s.topics) > 0 {
		committed := detectCommittedTopics(text, s.topics, s.turn)
		for _, topic := range committed {
			s.committedCounts[topic]++
		}
	}
}

func (a *Agent) maybeWarnSpiralHallucination() string {
	if a.spiralState == nil || a.spiralState.warnings >= spiralMaxWarnings {
		return ""
	}
	s := a.spiralState

	var spiraled []string
	for topic, count := range s.committedCounts {
		if count >= spiralMinCommittedTurns {
			spiraled = append(spiraled, topic)
		}
	}

	if len(spiraled) == 0 {
		return ""
	}

	s.warnings++

	if len(spiraled) > 5 {
		spiraled = spiraled[:5]
	}

	return fmt.Sprintf(
		"[spiral-of-hallucination] You expressed uncertainty about certain "+
			"topics in earlier turns, but are now treating them as established "+
			"fact without verification. Topics spiraling: %s.\n"+
			"This is the 'Spiral of Hallucination' pattern (arXiv:2601.15703): "+
			"early epistemic errors, once committed to context, propagate "+
			"irreversibly -- each subsequent turn deepens the commitment to an "+
			"unverified foundation. The agent's own hedged guesses from turn N "+
			"become treated as confirmed ground truth by turn N+2.\n"+
			"Before continuing:\n"+
			"1. Identify which prior assumptions still need verification\n"+
			"2. Run a test, diagnostic, or check to confirm or refute the "+
			"uncertain foundations\n"+
			"3. If an assumption is wrong, course-correct NOW before the "+
			"error propagates further into your edits\n"+
			"If the foundations have already been verified (test passed, "+
			"diagnostic confirmed), this warning can be safely ignored.",
		strings.Join(spiraled, ", "),
	)
}
