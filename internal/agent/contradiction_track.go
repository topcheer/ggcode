package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	contradictionMaxWarnings = 2

	contradictionThreshold = 2

	contradictionMaxClaims = 30

	contradictionMaxExcerpts = 4
)

type contradictionClaim struct {
	entity       string
	excerpt      string
	iteration    int
	acknowledged bool
	superseded   bool
	revision     bool
}

type contradictionInstance struct {
	priorClaim contradictionClaim
	newClaim   contradictionClaim
}

type contradictionState struct {
	claims         []contradictionClaim
	contradictions []contradictionInstance
	warnings       int
}

func newContradictionState() *contradictionState {
	return &contradictionState{}
}

func (s *contradictionState) reset() {
	s.claims = nil
	s.contradictions = nil
	s.warnings = 0
}

var contradictionClaimPatterns = []*regexp.Regexp{

	regexp.MustCompile(`(?i)\b(?:the\s+)?(?:bug|issue|problem|error|failure|crash)\s+is\s+(?:in|located in|found in|coming from|from)\s+([A-Za-z_][\w./-]{2,60})`),

	regexp.MustCompile(`(?i)\b(?:the\s+)?root cause\s+is\s+(?:(?:the|a|an)\s+)?([A-Za-z_][\w. /-]{2,60})`),

	regexp.MustCompile(`(?i)\b(?:the\s+)?source of (?:the\s+)?(?:problem|error|bug)\s+is\s+(?:(?:in|the|a|an)\s+)?([A-Za-z_][\w. /-]{2,60})`),

	regexp.MustCompile(`(?i)\b([A-Za-z]\w*(?:\s+\w+){0,4})\s+is\s+causing\s+(?:the\s+|a\s+|an\s+)?\w+`),

	regexp.MustCompile(`(?i)\b([A-Za-z]\w*(?:\s+\w+){0,4})\s+is\s+(?:the\s+)?(?:root cause|source of (?:the\s+)?(?:problem|error|bug))`),

	regexp.MustCompile(`(?i)\b(?:the\s+)?real (?:issue|problem|cause)\s+is\s+(?:in\s+)?([A-Za-z_][\w./-]{2,60})`),
}

var contradictionStopWords = map[string]bool{
	"that": true, "this": true, "the": true, "a": true, "an": true,
	"not": true, "yes": true, "no": true, "it": true, "our": true,
	"some": true, "most": true, "more": true, "very": true,
}

func normalizeContradictionEntity(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".")
	raw = strings.TrimSuffix(raw, ",")
	raw = strings.TrimSuffix(raw, "'s")
	raw = strings.ToLower(raw)

	raw = strings.TrimPrefix(raw, "the ")
	raw = strings.TrimPrefix(raw, "a ")
	raw = strings.TrimPrefix(raw, "an ")

	if contradictionStopWords[raw] {
		return ""
	}
	if len(raw) < 3 {
		return ""
	}

	if strings.Contains(raw, "/") {
		parts := strings.Split(raw, "/")
		raw = parts[len(parts)-1]
	}

	return raw
}

func extractContradictionExcerpt(text string, matchStart, matchEnd int) string {
	start := matchStart - 40
	if start < 0 {
		start = 0
	}
	end := matchEnd + 40
	if end > len(text) {
		end = len(text)
	}
	excerpt := strings.TrimSpace(text[start:end])
	if len(excerpt) > 120 {
		excerpt = excerpt[:120] + "..."
	}
	return excerpt
}

func extractClaims(text string, iteration int) []contradictionClaim {
	if len(text) < 15 {
		return nil
	}

	var claims []contradictionClaim
	seen := make(map[string]bool)

	lower := strings.ToLower(text)

	ackPositions := []int{}
	revisionPositions := []int{}
	for _, ack := range []string{"i was wrong", "i was mistaken", "i misread", "earlier i thought", "i previously thought", "let me correct", "correction:", "on second thought", "actually, i", "to correct myself"} {
		strong := ack != "actually, i"
		for start := 0; start < len(lower); {
			idx := strings.Index(lower[start:], ack)
			if idx < 0 {
				break
			}
			ackPositions = append(ackPositions, start+idx)
			if strong {
				revisionPositions = append(revisionPositions, start+idx)
			}
			start += idx + len(ack)
		}
	}
	nearAny := func(positions []int, entityStart int) bool {

		for _, pos := range positions {
			if pos <= entityStart && entityStart-pos <= 150 {
				return true
			}
		}
		return false
	}
	nearAck := func(entityStart int) bool { return nearAny(ackPositions, entityStart) }
	nearRevision := func(entityStart int) bool { return nearAny(revisionPositions, entityStart) }

	for _, pat := range contradictionClaimPatterns {
		locs := pat.FindAllStringSubmatchIndex(text, -1)
		for _, loc := range locs {
			if len(loc) < 4 {
				continue
			}
			entityStart, entityEnd := loc[2], loc[3]
			rawEntity := text[entityStart:entityEnd]
			entity := normalizeContradictionEntity(rawEntity)
			if entity == "" {
				continue
			}
			if seen[entity] {
				continue
			}
			seen[entity] = true

			excerpt := extractContradictionExcerpt(text, loc[0], loc[1])
			claims = append(claims, contradictionClaim{
				entity:       entity,
				excerpt:      excerpt,
				iteration:    iteration,
				acknowledged: nearAck(entityStart),
				revision:     nearRevision(entityStart),
			})
		}
	}

	return claims
}

func (s *contradictionState) recordContradictionClaims(text string, iteration int) {
	newClaims := extractClaims(text, iteration)

	hasRevision := false
	for _, nc := range newClaims {
		if nc.revision {
			hasRevision = true
		}
	}
	if hasRevision {

		reasserted := make(map[string]bool, len(newClaims))
		for _, nc := range newClaims {
			reasserted[nc.entity] = true
		}
		for i := range s.claims {
			if s.claims[i].iteration < iteration && !reasserted[s.claims[i].entity] {
				s.claims[i].superseded = true
			}
		}
	}
	for _, nc := range newClaims {
		if nc.acknowledged {
			continue
		}
		for _, pc := range s.claims {
			if pc.superseded {
				continue
			}

			if nc.entity == pc.entity {
				continue
			}
			if strings.Contains(nc.entity, pc.entity) || strings.Contains(pc.entity, nc.entity) {
				continue
			}

			s.contradictions = append(s.contradictions, contradictionInstance{
				priorClaim: pc,
				newClaim:   nc,
			})
			break
		}
	}

	for _, c := range newClaims {
		s.claims = append(s.claims, c)
	}
	if len(s.claims) > contradictionMaxClaims {
		s.claims = s.claims[len(s.claims)-contradictionMaxClaims:]
	}
}

func (a *Agent) maybeWarnContradiction(assistantText string, iteration int) string {
	if a.contradiction == nil {
		return ""
	}

	a.contradiction.recordContradictionClaims(assistantText, iteration)

	if a.contradiction.warnings >= contradictionMaxWarnings {
		return ""
	}

	total := len(a.contradiction.contradictions)
	if total < contradictionThreshold {
		return ""
	}

	a.contradiction.warnings++

	var excerpts []string
	startIdx := 0
	if total > contradictionMaxExcerpts {
		startIdx = total - contradictionMaxExcerpts
	}
	for i := startIdx; i < total && len(excerpts) < contradictionMaxExcerpts; i++ {
		c := a.contradiction.contradictions[i]
		excerpts = append(excerpts, fmt.Sprintf(
			"  - [iter %d] claimed \"%s\" vs [iter %d] claimed \"%s\"",
			c.priorClaim.iteration+1, c.priorClaim.entity,
			c.newClaim.iteration+1, c.newClaim.entity,
		))
	}

	msg := fmt.Sprintf("[CONTRADICTION-DETECTED] Found %d cross-turn contradiction(s) "+
		"in root-cause/location claims. The agent identified different sources for "+
		"the same issue across iterations without reconciling them.\nExamples:\n%s\n"+
		"Unacknowledged contradiction wastes iterations -- prior actions may have "+
		"targeted the wrong cause. Reconcile: which claim is correct, and what "+
		"evidence (tool output, test result) confirms it? Explicitly state which "+
		"earlier conclusion was wrong and why.",
		total, strings.Join(excerpts, "\n"))
	return msg
}
