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
//   - "Agent-Editing World Model" (AEWM, arXiv:2609.28416, Sep 2026): names
//     the failure mode TASK-STATE CONTAMINATION -- unsupported assumptions
//     and outdated conclusions persist in history and distort subsequent
//     decisions -- and argues a harness should revise the STATE underlying
//     later decisions rather than merely critique. This module's revision
//     ledger is the deterministic, inference-time analog: an acknowledged
//     correction records which standing beliefs it retired, and the revised
//     state rides on guidance the harness already injects.
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
//   - Revision ledger (AEWM State Revision): a STRONG acknowledged revision
//     ("I was wrong -- the root cause is B") records retired→current belief
//     pairs. Well-acknowledged revisions skip contradiction pairing (#1536
//     case C), so without the ledger they leave ZERO persistent trace and
//     the retired conclusion keeps contaminating later turns from raw
//     history. The ledger rides on existing guidance (warning footer) and,
//     when the warning channel is capped or below threshold, surfaces once
//     per run as a compact [State Revision] note.

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

	// contradictionMaxLedger: cap stored revision-ledger entries.
	contradictionMaxLedger = 6

	// contradictionMaxRetiredShown: max retired entities recorded per
	// revision claim and shown in a standalone revision note. The claims
	// that distort subsequent decisions are the most RECENT standing
	// beliefs, not the oldest ones.
	contradictionMaxRetiredShown = 2

	// contradictionMaxLedgerShown: max ledger entries in a warning footer.
	contradictionMaxLedgerShown = 3

	// contradictionMaxRevisionNotes: max standalone state-revision notes
	// per run (warnings carry the ledger footer instead).
	contradictionMaxRevisionNotes = 1
)

// contradictionClaim represents a single root-cause/location claim.
type contradictionClaim struct {
	entity    string // normalized claimed location/cause
	excerpt   string
	iteration int
	// acknowledged marks a claim that sits right after an ack phrase
	// (#1536 case C): it enters history but skips pairing this iteration.
	acknowledged bool
	// superseded marks a claim retired by an acknowledged revision
	// (#1536 case D): kept in history for audit, excluded from pairing.
	superseded bool
	// revision marks a claim following a STRONG revision phrase ("I was
	// wrong", "correction:", ...). A revision retires the superseded
	// prior claims (#1536 case D) so a later restatement of the corrected
	// assertion cannot pair against the stale one. Weak openers like
	// "actually, i" acknowledge without retiring anything.
	revision bool
}

// contradictionInstance represents a detected cross-turn reversal.
type contradictionInstance struct {
	priorClaim contradictionClaim
	newClaim   contradictionClaim
}

// revisionEntry records one acknowledged supersession: a standing belief
// that a strong revision ("I was wrong -- the root cause is B") retired in
// favor of a newer claim. Inference-time analog of AEWM's State Revision
// (arXiv:2609.28416).
type revisionEntry struct {
	retired     string
	current     string
	retiredIter int
	currentIter int
}

// contradictionState tracks claims and detected reversals across a run.
type contradictionState struct {
	claims         []contradictionClaim
	contradictions []contradictionInstance
	warnings       int
	// ledger records acknowledged supersessions (retired → current) so the
	// revised state can be carried forward on guidance already injected.
	ledger []revisionEntry
	// revisionNotesIssued bounds standalone state-revision notes per run.
	revisionNotesIssued int
}

func newContradictionState() *contradictionState {
	return &contradictionState{}
}

func (s *contradictionState) reset() {
	s.claims = nil
	s.contradictions = nil
	s.warnings = 0
	s.ledger = nil
	s.revisionNotesIssued = 0
}

// contradictionEntitiesConflict reports whether two normalized entities are
// distinct enough to count as competing claims -- the shared predicate of
// contradiction pairing and the revision ledger ("auth" vs "auth.go" are
// the same root, not competing claims).
func contradictionEntitiesConflict(a, b string) bool {
	if a == b {
		return false
	}
	return !strings.Contains(a, b) && !strings.Contains(b, a)
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
	// #1447-A + #1536 C/D: an ACKNOWLEDGED revision is not a contradiction -
	// but the old whole-text exemption ("return nil") blinded the detector
	// to every OTHER claim in the same message and dropped the corrected
	// claims from history ("actually, i" is an extremely common opener:
	// "Actually, I already ran the tests. The bug is in handler.go" washed
	// out the handler.go root-cause migration entirely). Claim-level
	// proximity instead: a claim is 'acknowledged' only when an ack phrase
	// sits within a window before it; acknowledged claims are recorded in
	// history (fixing the stale-pairing false positive when the corrected
	// assertion is later restated) but skip contradiction pairing this
	// iteration.
	ackPositions := []int{}
	revisionPositions := []int{}
	for _, ack := range []string{"i was wrong", "i was mistaken", "i misread", "earlier i thought", "i previously thought", "let me correct", "correction:", "on second thought", "actually, i", "to correct myself"} {
		strong := ack != "actually, i" // weak opener: acknowledges, does not retire
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
		// An ack phrase within 150 chars before the claim entity
		// acknowledges THAT claim, not one three paragraphs later.
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

// recordContradictionClaims adds new claims from the current iteration's text
// and detects contradictions against prior claims. Returns the number of NEW
// revision-ledger entries recorded this call (0 when no strong revision
// occurred).
func (s *contradictionState) recordContradictionClaims(text string, iteration int) int {
	newClaims := extractClaims(text, iteration)

	// Check new claims against prior claims for contradictions.
	// #1536 case C: only non-acknowledged claims pair; acknowledged ones
	// still enter history below (case D) so a later restatement of the
	// corrected assertion pairs against the CURRENT belief instead of the
	// superseded one.
	hasRevision := false
	for _, nc := range newClaims {
		if nc.revision {
			hasRevision = true
		}
	}
	if hasRevision {
		// #1536 case D: a strong revision retires prior claims not
		// re-asserted in the revision message - the agent's standing
		// belief moved; later restatements of the corrected assertion
		// must pair against the CURRENT belief, not the stale one.
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
	// AEWM State Revision ledger: a strong revision is a state change, not
	// just a new opinion -- record which standing beliefs it retired so the
	// harness can carry the revised state forward even when the warning
	// channel is capped or below threshold.
	newLedger := 0
	if hasRevision {
		newLedger = s.recordRevisionLedger(newClaims, iteration)
	}
	for _, nc := range newClaims {
		if nc.acknowledged {
			continue
		}
		for _, pc := range s.claims {
			if pc.superseded {
				continue // retired by an acknowledged revision
			}
			// A contradiction occurs when the entities are distinct enough to
			// be competing claims ("auth" vs "auth.go" are the same root).
			if !contradictionEntitiesConflict(nc.entity, pc.entity) {
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
	return newLedger
}

// recordRevisionLedger adds ledger entries for a strong revision: each
// revision-flagged claim retires the most recent standing beliefs it
// conflicts with (same pairing predicate as contradiction detection).
// Entries are deduped and bounded; returns the number of NEW entries.
func (s *contradictionState) recordRevisionLedger(newClaims []contradictionClaim, iteration int) int {
	seen := make(map[string]bool, len(s.ledger))
	for _, e := range s.ledger {
		seen[e.retired+"→"+e.current] = true
	}
	added := 0
	for _, nc := range newClaims {
		if !nc.revision {
			continue
		}
		retired := make(map[string]bool, contradictionMaxRetiredShown)
		// Scan backward: the beliefs that distort subsequent decisions are
		// the agent's most RECENT standing claims, not the oldest ones.
		for i := len(s.claims) - 1; i >= 0 && len(retired) < contradictionMaxRetiredShown; i-- {
			pc := s.claims[i]
			if retired[pc.entity] || !contradictionEntitiesConflict(nc.entity, pc.entity) {
				continue
			}
			retired[pc.entity] = true
			key := pc.entity + "→" + nc.entity
			if seen[key] {
				continue
			}
			seen[key] = true
			s.ledger = append(s.ledger, revisionEntry{
				retired:     pc.entity,
				current:     nc.entity,
				retiredIter: pc.iteration,
				currentIter: iteration,
			})
			added++
		}
	}
	if len(s.ledger) > contradictionMaxLedger {
		s.ledger = s.ledger[len(s.ledger)-contradictionMaxLedger:]
	}
	return added
}

// recentLedgerEntries returns up to n of the most recent ledger entries.
func (s *contradictionState) recentLedgerEntries(n int) []revisionEntry {
	if len(s.ledger) == 0 {
		return nil
	}
	if len(s.ledger) > n {
		return s.ledger[len(s.ledger)-n:]
	}
	return s.ledger
}

// maybeWarnContradiction checks for accumulated cross-turn contradictions
// and returns a guidance message. When the warning channel is silent
// (below threshold or capped), an acknowledged revision still surfaces once
// per run as a compact [State Revision] note -- the AEWM inference-time
// analog of revising the state underlying subsequent decisions.
func (a *Agent) maybeWarnContradiction(assistantText string, iteration int) string {
	if a.contradiction == nil {
		return ""
	}

	newLedger := a.contradiction.recordContradictionClaims(assistantText, iteration)

	total := len(a.contradiction.contradictions)
	if total >= contradictionThreshold && a.contradiction.warnings < contradictionMaxWarnings {
		a.contradiction.warnings++
		return a.contradiction.buildContradictionWarning(total)
	}

	if newLedger > 0 && a.contradiction.revisionNotesIssued < contradictionMaxRevisionNotes {
		a.contradiction.revisionNotesIssued++
		return formatRevisionNote(a.contradiction.recentLedgerEntries(contradictionMaxRetiredShown))
	}

	return ""
}

// buildContradictionWarning renders the standard contradiction warning and,
// when the revision ledger is non-empty, appends the revised state so the
// model reconciles against CURRENT beliefs, not just raw excerpt pairs. The
// footer piggybacks on this already-injected message -- no extra injection.
func (s *contradictionState) buildContradictionWarning(total int) string {
	// Build excerpts from recent contradictions.
	var excerpts []string
	startIdx := 0
	if total > contradictionMaxExcerpts {
		startIdx = total - contradictionMaxExcerpts
	}
	for i := startIdx; i < total && len(excerpts) < contradictionMaxExcerpts; i++ {
		c := s.contradictions[i]
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

	if footer := formatRevisionFooter(s.recentLedgerEntries(contradictionMaxLedgerShown)); footer != "" {
		msg += "\n" + footer
	}
	return msg
}

// formatRevisionFooter renders the revised-state block appended to
// contradiction warnings.
func formatRevisionFooter(entries []revisionEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Revised state (from your own acknowledged corrections):")
	for _, e := range entries {
		fmt.Fprintf(&sb, "\n  - \"%s\" (iter %d) was superseded by \"%s\" (iter %d) -- treat \"%s\" as retired.",
			e.retired, e.retiredIter+1, e.current, e.currentIter+1, e.retired)
	}
	return sb.String()
}

// formatRevisionNote renders the one-shot state-revision note emitted when
// the warning channel stays silent (acknowledged revisions skip pairing, so
// this is often the only persistent trace of the correction).
func formatRevisionNote(entries []revisionEntry) string {
	if len(entries) == 0 {
		return ""
	}
	e := entries[len(entries)-1]
	var sb strings.Builder
	fmt.Fprintf(&sb, "[State Revision] You explicitly corrected a prior conclusion: "+
		"your current standing belief is \"%s\" (iter %d), which supersedes \"%s\" (iter %d). "+
		"Treat \"%s\" as retired -- do not act on it or revert to it; build on \"%s\" "+
		"unless new tool evidence says otherwise.",
		e.current, e.currentIter+1, e.retired, e.retiredIter+1, e.retired, e.current)
	if len(entries) > 1 {
		sb.WriteString("\nAlso superseded by your corrections:")
		for _, p := range entries[:len(entries)-1] {
			fmt.Fprintf(&sb, "\n  - \"%s\" (iter %d) → \"%s\"", p.retired, p.retiredIter+1, p.current)
		}
	}
	return sb.String()
}
