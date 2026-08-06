package agent

// Scope Creep Detector
//
// Research basis:
//   - ATR-2026-00041 "Agent Scope Creep Detection" (agentthreatrule.org, 2026):
//     Detects when an agent gradually expands its operational boundaries
//     beyond the initial assignment. The rule identifies language patterns
//     of unsolicited scope expansion: "while I'm at it, let me also...",
//     "I've taken the liberty to...", "I also need access to...", etc.
//   - CSA "AI Agent Security Starts with Scope Control" (2026):
//     "AI-generated code now accounts for 42% of all committed code... an
//     agent producing anomalous output touches a material fraction of your
//     release surface before any human sees the pull request."
//   - Re-entry Blog "AI Coding Agent Runaway Detection" (2026):
//     Scope creep is a leading signal of agent runaway - the agent makes
//     unsolicited changes beyond the user's request, increasing blast radius.
//
// Problem: AI coding agents frequently expand beyond the user's request by
// performing additional unsolicited modifications:
//
//  1. "While I'm at it, let me also refactor the auth module" - unsolicited
//     refactor unrelated to the original bug fix
//  2. "I've gone ahead and updated all the test files" - expanding to files
//     the user never asked to modify
//  3. "I also cleaned up the imports and reformatted everything" - cosmetic
//     changes that inflate the diff and review burden
//  4. "Since I was already in this file, I fixed some other issues too" -
//     leveraging proximity as justification for unsolicited work
//
// Each unsolicited change increases the diff size, review burden, and risk
// of introducing regressions in code the user didn't want touched.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - file_churn_detect.go: tracks repeated edits to the same file, not
//     unsolicited expansion to new files/areas.
//   - edit_blast_radius.go: measures structural impact of edits, not
//     whether the edits were requested.
//   - premature_commitment.go: checks evidence before first edit, not
//     scope expansion after initial work.
//
// Gap: No detector scans assistant text for language indicating the agent
// is expanding beyond the user's request. This detector addresses the gap
// by identifying scope-creep language patterns in assistant responses.
//
// Design:
//   - Scans assistant text for scope expansion language patterns
//   - Patterns derived from ATR-2026-00041 and adapted for coding context
//   - Threshold: 2+ scope creep signals → inject guidance to stay focused
//   - Zero LLM cost - pure deterministic text pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// scopeCreepWarnThreshold: at this count of signals, inject guidance.
	scopeCreepWarnThreshold = 2

	// scopeCreepMaxWarnings: max warnings per run.
	scopeCreepMaxWarnings = 2

	// scopeCreepMaxExcerpts: max signal excerpts to include in hint.
	scopeCreepMaxExcerpts = 3
)

// scopeCreepPattern represents a detected scope-creep signal.
type scopeCreepPattern struct {
	category string // "action" or "justification"
	pattern  *regexp.Regexp
}

// Precompiled patterns adapted from ATR-2026-00041 for coding agents.
// Case-insensitive.
var scopeCreepPatterns = []scopeCreepPattern{
	// Unsolicited additional actions - "while I'm at it" style
	{"action", regexp.MustCompile(`(?i)(?:while\s+i(?:'m|\s+am)\s+at\s+it|since\s+i(?:'m|\s+am)\s+(?:already\s+)?here|while\s+i(?:'m|\s+am)\s+in\s+(?:this|the)\s+(?:file|area|section))`)},
	{"action", regexp.MustCompile(`(?i)(?:i(?:'ll|\s+will)\s+also|let\s+me\s+also|i\s+should\s+also|i\s+also\s+(?:went|fixed|updated|cleaned|refactored))`)},
	{"action", regexp.MustCompile(`(?i)(?:i(?:'ve|\s+have)\s+(?:also\s+)?(?:gone\s+ahead|taken\s+the\s+liberty|decided)\s+(?:and\s+)?(?:to\s+)?(?:fix|update|clean|refactor|modify|change|add|remove))`)},
	{"action", regexp.MustCompile(`(?i)(?:i\s+(?:also\s+)?(?:cleaned\s+up|tidied\s+up|fixed\s+up)\s+(?:the\s+)?(?:imports?|formatting|whitespace|naming|comments?))`)},

	// Scope expansion justification - rationalizing going beyond request
	{"justification", regexp.MustCompile(`(?i)(?:to\s+(?:fully|properly|better|completely|thoroughly)\s+(?:fix|handle|address|solve)\s+(?:this|that),?\s+i\s+(?:also\s+)?(?:need\s+to|had\s+to|must)\s+(?:fix|update|change|modify|refactor))`)},
	{"justification", regexp.MustCompile(`(?i)(?:it\s+(?:would\s+)?(?:also\s+)?be\s+(?:good|helpful|useful|better|wise)\s+to\s+(?:also\s+)?(?:fix|update|clean|refactor|review))`)},
	{"justification", regexp.MustCompile(`(?i)(?:i\s+(?:noticed|found|spotted|saw)\s+(?:some|a\s+few|several)\s+(?:other|additional)\s+(?:issues?|problems?|things)\s+(?:that\s+)?(?:i\s+)?(?:also\s+)?(?:fixed|should|could))`)},
	{"justification", regexp.MustCompile(`(?i)(?:expanding\s+(?:my|the)\s+(?:scope|changes?|search)\s+to\s+(?:include|cover|also))`)},
	{"justification", regexp.MustCompile(`(?i)(?:i\s+went\s+(?:ahead\s+)?beyond\s+(?:the\s+)?(?:original|assigned|initial)\s+(?:scope|task|request))`)},
}

// scopeCreepHit records a single detected scope-creep signal.
type scopeCreepHit struct {
	category string
	excerpt  string
}

// scopeCreepState tracks scope-creep detections across a run.
type scopeCreepState struct {
	warnings int
}

func newScopeCreepState() *scopeCreepState {
	return &scopeCreepState{}
}

func (s *scopeCreepState) reset() {
	s.warnings = 0
}

// scanScopeCreep analyzes assistant text for scope-creep language patterns.
func scanScopeCreep(text string) []scopeCreepHit {
	if len(text) == 0 {
		return nil
	}

	var hits []scopeCreepHit
	seen := make(map[string]bool) // deduplicate by excerpt

	for _, sp := range scopeCreepPatterns {
		locs := sp.pattern.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			start := loc[0]
			excerptStart := start - 20
			if excerptStart < 0 {
				excerptStart = 0
			}
			excerptEnd := loc[1] + 40
			if excerptEnd > len(text) {
				excerptEnd = len(text)
			}
			excerpt := strings.TrimSpace(text[excerptStart:excerptEnd])
			if len(excerpt) > 80 {
				excerpt = excerpt[:80] + "..."
			}

			key := sp.category + ":" + excerpt
			if seen[key] {
				continue
			}
			seen[key] = true

			hits = append(hits, scopeCreepHit{
				category: sp.category,
				excerpt:  excerpt,
			})
		}
	}

	return hits
}

// maybeWarnScopeCreep checks assistant text for scope-creep language and
// returns a guidance message if the threshold is exceeded.
// Returns empty string if no warning is needed.
func (a *Agent) maybeWarnScopeCreep(text string) string {
	if a.scopeCreep == nil {
		return ""
	}
	if a.scopeCreep.warnings >= scopeCreepMaxWarnings {
		return ""
	}

	hits := scanScopeCreep(text)
	if len(hits) < scopeCreepWarnThreshold {
		return ""
	}

	actionCount := 0
	for _, h := range hits {
		if h.category == "action" {
			actionCount++
		}
	}

	a.scopeCreep.warnings++

	var excerpts []string
	for _, h := range hits {
		if len(excerpts) >= scopeCreepMaxExcerpts {
			break
		}
		excerpts = append(excerpts, fmt.Sprintf("  [%s] ...%s...", h.category, h.excerpt))
	}

	header := fmt.Sprintf(
		"[scope-creep] Detected %d signal(s) of unsolicited scope expansion "+
			"(%d unsolicited actions, %d justifications for expanding scope). "+
			"Making changes beyond the user's request increases diff size, review burden, "+
			"and regression risk in code the user didn't ask to modify. "+
			"Stick to the requested task. If you spot a genuinely related issue, note it "+
			"for the user rather than fixing it unprompted. "+
			"Avoid 'while I'm at it' expansions.\n",
		len(hits), actionCount, len(hits)-actionCount,
	)
	return header + "Detected signals:\n" + strings.Join(excerpts, "\n")
}
