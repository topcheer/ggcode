package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	constraintReminderMinIterations = 12

	constraintMaxWarnings = 1

	constraintEditRecencyTurns = 2

	constraintMaxTracked = 8

	constraintExcerptLen = 120
)

type constraintEntry struct {
	excerpt    string
	messageIdx int
}

type constraintAmnesiaState struct {
	constraints  []constraintEntry
	warnings     int
	currentIter  int
	lastEditIter int
}

func newConstraintAmnesiaState() *constraintAmnesiaState {
	return &constraintAmnesiaState{}
}

func (s *constraintAmnesiaState) reset() {
	s.constraints = nil
	s.warnings = 0
	s.currentIter = 0
	s.lastEditIter = -1
}

func (s *constraintAmnesiaState) noteEdit(iter int) {
	if iter > s.lastEditIter {
		s.lastEditIter = iter
	}
}

var constraintPatterns = []*regexp.Regexp{

	regexp.MustCompile(`(?i)(?:don'?t|do not|never|no new|avoid|must not|should not)\b[^\n.]{5,80}`),

	regexp.MustCompile(`(?i)\b(?:only|must|always|exclusively)\s+(?:use|modify|touch|edit|change|add|import|call)\b[^\n.]{3,60}`),

	regexp.MustCompile(`(?i)\b(?:needs? to be|has to be|it'?s important that|make sure|ensure that)\b[^\n.]{5,70}`),

	regexp.MustCompile(`(?i)\b(?:stay in|limit (?:to|changes to)|keep within|don'?t touch|leave\b[^\n.]{0,20}alone)\b[^\n.]{3,60}`),
}

func extractConstraints(text string) []string {
	if len(text) == 0 {
		return nil
	}

	text = stripNonDirectiveText(text)

	var results []string
	seen := make(map[string]bool)

	for _, pat := range constraintPatterns {
		matches := pat.FindAllString(text, -1)
		for _, m := range matches {
			excerpt := strings.TrimSpace(m)

			if len(excerpt) > constraintExcerptLen {
				runes := []rune(excerpt)
				cut := constraintExcerptLen
				if cut > len(runes) {
					cut = len(runes)
				}
				excerpt = string(runes[:cut]) + "..."
			}

			key := strings.ToLower(excerpt)
			if len(key) > 40 {
				key = key[:40]
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, excerpt)
		}
	}

	return results
}

func (s *constraintAmnesiaState) recordConstraints(text string, messageIdx int) {
	if s == nil {
		return
	}
	extracted := extractConstraints(text)
	for _, ex := range extracted {
		if len(s.constraints) >= constraintMaxTracked {
			break
		}
		s.constraints = append(s.constraints, constraintEntry{
			excerpt:    ex,
			messageIdx: messageIdx,
		})
	}
}

func (s *constraintAmnesiaState) maybeWarn(iteration int) string {
	if s == nil || len(s.constraints) == 0 {
		return ""
	}
	if s.warnings >= constraintMaxWarnings {
		return ""
	}

	s.currentIter = iteration

	earliestMsgIdx := s.constraints[0].messageIdx
	iterationsSince := iteration - earliestMsgIdx
	if iterationsSince < constraintReminderMinIterations {
		return ""
	}

	if s.lastEditIter < 0 || iteration-s.lastEditIter > constraintEditRecencyTurns {
		return ""
	}

	s.warnings++

	var lines []string
	for i, c := range s.constraints {
		lines = append(lines, fmt.Sprintf("  %d. \"%s\"", i+1, c.excerpt))
	}

	return fmt.Sprintf(
		"[constraint-reminder] You are %d iterations into this task. "+
			"Constraints established early in the conversation may have "+
			"scrolled out of your active attention. Before making your next "+
			"edit, verify compliance with these user-specified constraints:\n%s\n"+
			"If any pending action would violate a constraint, stop and reconsider.",
		iterationsSince,
		strings.Join(lines, "\n"),
	)
}

func (s *constraintAmnesiaState) markUndelivered() {
	if s.warnings > 0 {
		s.warnings--
	}
}
