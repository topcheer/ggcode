package agent

// Cross-Turn State Contradiction Detector
//
// Research basis:
//   - "Self-Contra: Self-Contradictory Hallucinations of Large Language Models"
//     (EMNLP 2024 Findings, arXiv 2305.15852): shows that LLMs frequently
//     produce self-contradictory statements within the same conversation --
//     asserting X at one point and asserting not-X (or a different X) later,
//     without acknowledging the reversal.
//   - "LLM-as-Judge in Production" (Zylos, 2026): intrinsic self-correction
//     without external grounding is unreliable; contradictions compound because
//     the agent never re-checks earlier claims against new evidence.
//   - SWE-bench trajectory analysis: ~12% of failing trajectories contain
//     root-cause reversals where the agent identifies the bug in file A, then
//     later identifies the bug in file B without explaining why the earlier
//     conclusion was wrong.
//
// Problem: AI coding agents often make a definitive root-cause or location
// claim in one turn ("the bug is in auth.go", "the issue is caused by the
// config parser"), then in a later turn make a DIFFERENT claim about the same
// issue ("the real problem is in router.go", "actually the timeout is caused
// by the retry loop") -- without acknowledging the contradiction. This wastes
// iterations because the agent may have acted on the now-contradicted claim
// (edited the wrong file, applied a fix that didn't address the real cause).
//
// Distinct from existing detectors:
//   - circular_reasoning.go: detects tautological justification (correct
//     because it's correct). Contradiction detection tracks factual reversals
//     across turns, not structural vacuity.
//   - selective_evidence.go: detects confirmation bias (emphasizing positives
//     while dismissing negatives). Contradiction detection is about the agent
//     disagreeing with its OWN prior statement.
//   - unverified_claim.go: detects success claims without verification.
//     Contradiction detection catches the opposite: two conflicting claims
//     about root cause.
//   - drift_recurrence.go: detects topic drift. Contradiction detection is
//     about the same topic but conflicting conclusions.
//
// Design:
//   - Scans assistant text for root-cause / issue-location claim patterns:
//     1. "the bug/issue/problem/error is in <X>"
//     2. "the root cause is <X>"
//     3. "<X> is causing the <Y>"
//     4. "the error comes from <X>"
//   - Normalizes the claimed entity (file/module/function name)
//   - Tracks distinct claims; when a NEW claim contradicts a prior one,
//     records a contradiction instance
//   - When 2+ contradictions accumulate, injects guidance to reconcile
//   - Zero LLM cost -- pure deterministic pattern matching
//   - Non-blocking advisory, max 2 warnings per run

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// contradictionMaxWarnings: max warnings per run.
	contradictionMaxWarnings = 2

	// contradictionThreshold: contradictions before warning.
	contradictionThreshold = 2

	// contradictionMaxClaims: cap stored claims to bound memory.
	contradictionMaxClaims = 30

	// contradictionMaxExcerpts: max excerpts shown in warning.
	contradictionMaxExcerpts = 4
)

// contradictionClaim represents a single root-cause/location claim.
type contradictionClaim struct {
	entity    string // normalized claimed location/cause
	excerpt   string
	iteration int
}

// contradictionInstance represents a detected cross-turn reversal.
type contradictionInstance struct {
	priorClaim contradictionClaim
	newClaim   contradictionClaim
}

// contradictionState tracks claims and detected reversals across a run.
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

// Root-cause / issue-location claim patterns.
// Each captures group 1 = the claimed entity (file, module, function, or noun phrase).
var contradictionClaimPatterns = []*regexp.Regexp{
	// "the bug/issue/problem/error/failure is in <X>"
	regexp.MustCompile(`(?i)\b(?:the\s+)?(?:bug|issue|problem|error|failure|crash)\s+is\s+(?:in|located in|found in|coming from|from)\s+([A-Za-z_][\w./-]{2,60})`),

	// "the root cause is <X>" (optionally skip "the"/"a"/"an" article)
	regexp.MustCompile(`(?i)\b(?:the\s+)?root cause\s+is\s+(?:(?:the|a|an)\s+)?([A-Za-z_][\w. /-]{2,60})`),

	// "the source of the problem is <X>"
	regexp.MustCompile(`(?i)\b(?:the\s+)?source of (?:the\s+)?(?:problem|error|bug)\s+is\s+(?:(?:in|the|a|an)\s+)?([A-Za-z_][\w. /-]{2,60})`),

	// "<X> is causing the <Y>" (multi-word entity, flexible Y)
	regexp.MustCompile(`(?i)\b([A-Za-z]\w*(?:\s+\w+){0,4})\s+is\s+causing\s+(?:the\s+|a\s+|an\s+)?\w+`),

	// "<X> is the root cause / source of the problem"
	regexp.MustCompile(`(?i)\b([A-Za-z]\w*(?:\s+\w+){0,4})\s+is\s+(?:the\s+)?(?:root cause|source of (?:the\s+)?(?:problem|error|bug))`),

	// "the real issue is in <X>" (explicitly revising)
	regexp.MustCompile(`(?i)\b(?:the\s+)?real (?:issue|problem|cause)\s+is\s+(?:in\s+)?([A-Za-z_][\w./-]{2,60})`),
}

// stopWords for entity normalization -- these are common words that should
// not be treated as meaningful entities.
var contradictionStopWords = map[string]bool{
	"that": true, "this": true, "the": true, "a": true, "an": true,
	"not": true, "yes": true, "no": true, "it": true, "our": true,
	"some": true, "most": true, "more": true, "very": true,
}

// normalizeContradictionEntity normalizes a claimed entity for comparison.
// Extracts the core identifier (file/module/function name).
func normalizeContradictionEntity(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".")
	raw = strings.TrimSuffix(raw, ",")
	raw = strings.TrimSuffix(raw, "'s")
	raw = strings.ToLower(raw)

	// Strip leading articles from multi-word entities.
	raw = strings.TrimPrefix(raw, "the ")
	raw = strings.TrimPrefix(raw, "a ")
	raw = strings.TrimPrefix(raw, "an ")

	if contradictionStopWords[raw] {
		return ""
	}
	if len(raw) < 3 {
		return ""
	}

	// If it looks like a file path, keep the last component for comparison.
	if strings.Contains(raw, "/") {
		parts := strings.Split(raw, "/")
		raw = parts[len(parts)-1]
	}

	return raw
}

// extractContradictionExcerpt returns a trimmed excerpt around a match.
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

// extractClaims finds all root-cause/location claims in text.
func extractClaims(text string, iteration int) []contradictionClaim {
	if len(text) < 15 {
		return nil
	}

	var claims []contradictionClaim
	seen := make(map[string]bool) // deduplicate by entity within same text

	// #1447-A: an ACKNOWLEDGED revision is not a contradiction - the
	// "real issue is in X" pattern's own comment says "(explicitly
	// revising)", yet the claim was still recorded and paired against the
	// superseded one: "Earlier I thought A, but I was wrong - the root
	// cause is B" counted as a contradiction against the detector's own
	// 'without acknowledging' charter. Skip texts that explicitly
	// acknowledge the revision.
	lower := strings.ToLower(text)
	for _, ack := range []string{"i was wrong", "i was mistaken", "i misread", "earlier i thought", "i previously thought", "let me correct", "correction:", "on second thought", "actually, i", "to correct myself"} {
		if strings.Contains(lower, ack) {
			return nil
		}
	}

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
				entity:    entity,
				excerpt:   excerpt,
				iteration: iteration,
			})
		}
	}

	return claims
}

// recordContradictionClaims adds new claims from the current iteration's text
// and detects contradictions against prior claims.
func (s *contradictionState) recordContradictionClaims(text string, iteration int) {
	newClaims := extractClaims(text, iteration)

	// Check new claims against prior claims for contradictions.
	for _, nc := range newClaims {
		for _, pc := range s.claims {
			// A contradiction occurs when the new entity differs from a prior
			// entity AND they aren't substrings of each other (e.g. "auth" vs
			// "auth.go" are the same root, not a contradiction).
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
			break // only record one contradiction per new claim
		}
	}

	// Append new claims to history (bounded).
	for _, c := range newClaims {
		s.claims = append(s.claims, c)
	}
	if len(s.claims) > contradictionMaxClaims {
		s.claims = s.claims[len(s.claims)-contradictionMaxClaims:]
	}
}

// maybeWarnContradiction checks for accumulated cross-turn contradictions
// and returns a guidance message. Returns empty string if no warning is needed.
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

	// Build excerpts from recent contradictions.
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
