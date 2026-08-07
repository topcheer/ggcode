package agent

// Deferred Work Tracker - Forgotten Follow-up Detector
//
// Research basis:
//   - Microsoft AI Red Team 2026 taxonomy: "goal completion gaps" - agents
//     that partially complete tasks, saying they'll handle remaining items
//     later but never circling back. This is one of the most common
//     production failure modes.
//   - IBM STRATUS (NeurIPS 2025): "Transactional No-Regression" (TNR)
//     principle - every deferred action must be completed before the
//     agent can advance. Unfinished deferrals are treated as quality
//     failures, not optional polish.
//   - Anthropic Context Engineering (Sep 2025): as context windows fill,
//     earlier commitments are forgotten - "attention fading" causes the
//     agent to lose track of items it promised to address later.
//   - AgentMarketCap 2026 production report: incomplete tasks shipped as
//     "done" is a top-3 source of user-reported agent failures.
//
// Problem: AI coding agents frequently defer work to "later" and then
// never return to complete it:
//
//  1. "I'll add error handling in the next iteration" - never added
//  2. "We can handle the edge case after the main logic" - forgotten
//  3. "I'll write tests for this in a follow-up" - no tests written
//  4. "Let me finish the main function first, then add validation" - skipped
//
// Each deferral creates an implicit TODO. If the agent doesn't circle
// back, the work is silently dropped. Users receive "complete" output
// that is actually incomplete.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - verification_debt.go: tracks missing build/test verification, not
//     textual promises to do work later.
//   - assumption_track.go: detects unverified assumptions, not deferred
//     work items.
//   - fulfillment_gate.go: checks if user's request was addressed, not
//     whether self-promised follow-ups were completed.
//   - scope_creep.go: detects scope expansion, not scope contraction
//     from forgotten deferrals.
//
// Gap: No detector tracks deferred work items across iterations to
// verify they are eventually completed. This detector addresses the gap
// by maintaining a ledger of deferred items and alerting when they
// remain unaddressed.
//
// Design:
//   - Scans assistant text for deferral language patterns
//   - Records each deferral with the iteration it was made
//   - On subsequent iterations, checks if the deferral was addressed
//   - If deferrals remain open after N iterations, injects guidance
//   - If the agent declares completion with open deferrals, warns
//   - Zero LLM cost - pure deterministic text pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// deferredMaxWarnings: max warnings per run.
	deferredMaxWarnings = 2

	// deferredAgeThreshold: warn after this many iterations unaddressed.
	deferredAgeThreshold = 2

	// deferredMaxItems: max items to track (memory bound).
	deferredMaxItems = 10

	// deferredMaxExcerpts: max excerpts to show in warning.
	deferredMaxExcerpts = 5
)

// deferralPattern matches a deferral language pattern.
type deferralPattern struct {
	id      string
	pattern *regexp.Regexp
}

// Precompiled patterns for detecting deferral language. Case-insensitive.
// These match phrases where the agent explicitly says it will do
// something later/after/next rather than now.
var deferralPatterns = []deferralPattern{
	// Explicit "later" deferrals
	{"later", regexp.MustCompile(`(?i)\b(?:I(?:'ll| will)|we(?:'ll| will))\s+(?:do|handle|add|fix|address|implement|update|create|write|refactor|test).*\blater\b`)},
	{"later_phrase", regexp.MustCompile(`(?i)\b(?:do|handle|add|fix|address|implement|update|create|write|refactor|test).*\blater\b`)},

	// "Next" iteration/step deferrals
	{"next_iter", regexp.MustCompile(`(?i)\b(?:in|on|at)\s+(?:the\s+)?next\s+(?:iteration|step|pass|round|turn)\b`)},

	// "After this" sequencing deferrals
	{"after_this", regexp.MustCompile(`(?i)\bafter\s+(?:this|that|we|I)\b.*\b(?:do|handle|add|fix|address|implement|finish|complete|update|create|write)\b`)},
	{"then_do", regexp.MustCompile(`(?i)\bthen\s+(?:I(?:'ll| will)?|we(?:'ll| will)?)?\s*(?:do|handle|add|fix|address|implement|update|create|write|refactor)\b`)},

	// "Follow-up" deferrals
	{"follow_up", regexp.MustCompile(`(?i)\b(?:in|as|on)\s+(?:a\s+)?follow[\s-]?up\b`)},

	// "Come back" deferrals
	{"come_back", regexp.MustCompile(`(?i)\bcome back to\s+(?:this|that|it|the)\b`)},

	// "Still need to" / "still need to be" - acknowledging incomplete work
	{"still_need", regexp.MustCompile(`(?i)\bstill need(?:s)? to be\s+(?:done|handled|addressed|implemented|added|fixed|completed)\b`)},
	{"still_need_to", regexp.MustCompile(`(?i)\bstill need to\s+(?:do|handle|add|fix|address|implement|update|create|write|test)\b`)},

	// "Remaining" work acknowledgment
	{"remaining_work", regexp.MustCompile(`(?i)\bremaining\s+(?:work|items|tasks|steps)\b`)},

	// "TODO" markers in prose (not code comments)
	{"todo_prose", regexp.MustCompile(`(?i)\bTODO:\s*(?:handle|add|fix|address|implement|update|create|write|test|refactor)\b`)},

	// "For now" - partial implementation deferral
	{"for_now", regexp.MustCompile(`(?i)\bfor now\b.*\b(?:but|and|then|we|I)\b.*\b(?:later|after|next|follow)\b`)},

	// "Once X is done, I'll Y" - conditional deferral
	{"once_done", regexp.MustCompile(`(?i)\bonce\s+(?:that|this|it|we|I)\s+(?:is|are|'s)?\s*(?:done|complete|finished|working)\b`)},

	// "Skip for now" - explicit work skipping
	{"skip_now", regexp.MustCompile(`(?i)\bskip(?:ping)?\b.*\b(?:for now|for the moment|later)\b`)},
}

// completionPattern matches language indicating the agent believes the
// task is complete or is wrapping up.
var completionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:I(?:'ve| have)|we(?:'ve| have))\s+(?:completed|finished|done|implemented|addressed)\b`),
	regexp.MustCompile(`(?i)\b(?:the|this|that)\s+(?:task|work|change|feature|fix)\s+(?:is|are)\s+(?:complete|done|finished|ready)\b`),
	regexp.MustCompile(`(?i)\bthat(?:'s| is)\s+it\b`),
	regexp.MustCompile(`(?i)\ball\s+(?:set|done|complete)\b`),
}

// resolutionPattern matches language indicating a previously deferred
// item has been addressed.
var resolutionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bnow\s+(?:let(?:'s| us| me)|I(?:'ll| will)|we(?:'ll| will))\s*(?:do|handle|add|fix|address|implement|update|create|write|test|refactor)\b`),
	regexp.MustCompile(`(?i)\b(?:going|back|returning)\s+(?:back\s+)?to\s+(?:the|that|this)\b`),
	regexp.MustCompile(`(?i)\b(?:I(?:'ve| have)|we(?:'ve| have))\s+(?:now\s+)?(?:handled|addressed|added|fixed|implemented|completed|updated|created|written)\s+(?:the\s+)?(?:remaining|deferred|earlier)\b`),
	regexp.MustCompile(`(?i)\bas promised\b`),
	regexp.MustCompile(`(?i)\b(?:earlier|before)\s+I mentioned\b`),
}

// deferredItem represents a single tracked deferral.
type deferredItem struct {
	patternID string
	excerpt   string
	iteration int  // 0-based iteration when detected
	resolved  bool // true if addressed in a later iteration
}

// deferredWorkState tracks deferrals across a run.
type deferredWorkState struct {
	items    []deferredItem
	warnings int
	maxIter  int // highest iteration seen
}

func newDeferredWorkState() *deferredWorkState {
	return &deferredWorkState{}
}

func (s *deferredWorkState) reset() {
	s.items = nil
	s.warnings = 0
	s.maxIter = 0
}

// scanDeferrals analyzes text for deferral language and returns matches.
func scanDeferrals(text string) []deferredItem {
	if len(text) == 0 {
		return nil
	}

	var items []deferredItem
	seen := make(map[string]bool) // deduplicate

	for _, dp := range deferralPatterns {
		locs := dp.pattern.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			start := loc[0]
			excerptStart := start - 15
			if excerptStart < 0 {
				excerptStart = 0
			}
			excerptEnd := loc[1] + 50
			if excerptEnd > len(text) {
				excerptEnd = len(text)
			}
			excerpt := strings.TrimSpace(text[excerptStart:excerptEnd])
			if len(excerpt) > 90 {
				excerpt = excerpt[:90] + "..."
			}

			// Deduplicate by excerpt.
			key := dp.id + ":" + excerpt
			if seen[key] {
				continue
			}
			seen[key] = true

			items = append(items, deferredItem{
				patternID: dp.id,
				excerpt:   excerpt,
			})
		}
	}

	return items
}

// hasCompletionLanguage checks if text contains task-completion language.
func hasCompletionLanguage(text string) bool {
	for _, p := range completionPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

// hasResolutionLanguage checks if text indicates deferred items are being
// addressed now.
func hasResolutionLanguage(text string) bool {
	for _, p := range resolutionPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

// recordDeferrals adds new deferral items from the current iteration's
// text. If resolution language is present, marks existing items as resolved.
func (s *deferredWorkState) recordDeferrals(text string, iteration int) {
	if s.maxIter < iteration {
		s.maxIter = iteration
	}

	// Check for resolution language first - if present, resolve items.
	if hasResolutionLanguage(text) {
		for i := range s.items {
			s.items[i].resolved = true
		}
	}

	// Record new deferrals from this iteration.
	newItems := scanDeferrals(text)
	for _, item := range newItems {
		if len(s.items) >= deferredMaxItems {
			break
		}
		item.iteration = iteration
		s.items = append(s.items, item)
	}
}

// openDeferrals returns deferral items that remain unresolved and older
// than the age threshold.
func (s *deferredWorkState) openDeferrals(currentIter int) []deferredItem {
	var open []deferredItem
	for _, item := range s.items {
		if item.resolved {
			continue
		}
		age := currentIter - item.iteration
		if age >= deferredAgeThreshold {
			open = append(open, item)
		}
	}
	return open
}

// maybeWarnDeferredWork checks if there are stale deferred items and
// returns a guidance message. Returns empty string if no warning needed.
func (a *Agent) maybeWarnDeferredWork(assistantText string, iteration int) string {
	if a.deferredWork == nil {
		return ""
	}

	// Record deferrals from this iteration's text.
	a.deferredWork.recordDeferrals(assistantText, iteration)

	if a.deferredWork.warnings >= deferredMaxWarnings {
		return ""
	}

	// Check for stale open deferrals.
	open := a.deferredWork.openDeferrals(iteration)
	if len(open) == 0 {
		// Check for completion-with-open-deferrals case.
		if hasCompletionLanguage(assistantText) {
			allOpen := 0
			for _, item := range a.deferredWork.items {
				if !item.resolved {
					allOpen++
				}
			}
			if allOpen == 0 {
				return ""
			}
			// Agent says "done" but has open deferrals - warn immediately.
			open = make([]deferredItem, 0, allOpen)
			for _, item := range a.deferredWork.items {
				if !item.resolved {
					open = append(open, item)
				}
			}
		} else {
			return ""
		}
	}

	a.deferredWork.warnings++

	// Build excerpts list.
	var excerpts []string
	for _, item := range open {
		if len(excerpts) >= deferredMaxExcerpts {
			break
		}
		excerpts = append(excerpts, fmt.Sprintf("  - [iter %d] ...%s...", item.iteration+1, item.excerpt))
	}

	isCompletion := hasCompletionLanguage(assistantText)

	if isCompletion {
		return fmt.Sprintf("[DEFERRED-WORK] You appear to be wrapping up, "+
			"but %d deferred work item(s) from earlier iterations remain "+
			"unaddressed. Do not declare the task complete until these items "+
			"are handled.\nOutstanding deferrals:\n%s\n"+
			"Go back and complete each item now.",
			len(open), strings.Join(excerpts, "\n"))
	}

	return fmt.Sprintf("[DEFERRED-WORK] Detected %d deferred work item(s) "+
		"that remain unaddressed. Each deferral is an implicit promise - "+
		"if you don't circle back, the work is silently dropped and the "+
		"task is incomplete.\nOutstanding deferrals:\n%s\n"+
		"Address these items now before declaring the task complete.",
		len(open), strings.Join(excerpts, "\n"))
}
