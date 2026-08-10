package agent

// Assumption Tracker - Implicit Assumption Detector
//
// Research basis:
//   - Luum Cognitive OS: "Assumption Tracking" rule - detects when agents
//     make assumptions instead of working from verified requirements. High
//     assumption counts indicate the agent is guessing rather than executing
//     from clear specifications.
//   - Calibration literature (CogCal-1, 2025): "A model achieving 75%
//     accuracy while expressing maximum confidence when wrong is more
//     dangerous than a 60%-accurate model that knows its limits."
//   - CodeCoR (arXiv 2501.07811, Jan 2025): self-reflective frameworks
//     that evaluate agent effectiveness show unverified assumptions are
//     a leading indicator of quality problems.
//
// Problem: AI coding agents frequently fill in gaps by making implicit
// assumptions instead of verifying requirements or asking the user:
//
//  1. "I assume the database is PostgreSQL" - unverified tech stack
//  2. "I'll assume the default port is 3000" - unverified configuration
//  3. "This probably needs a migration" - acting on unverified belief
//  4. "Based on context, I used the repository pattern" - inferring
//     architecture without confirmation
//
// Each assumption is a point where reality could differ from what the
// agent guessed. Multiple assumptions compound: 5 assumptions in one
// task means 5 points of potential failure.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - premature_commitment.go: checks if first edit happened too quickly
//     (evidence-based), not about language expressing assumptions.
//   - confidence.go: trajectory-level tool success rates, not text
//     content analysis.
//   - tool_output_claim_verify.go: checks if agent misread tool output,
//     not about unstated assumptions.
//
// Gap: No detector scans assistant text for implicit assumption language
// that indicates the agent is guessing rather than verifying. This
// detector addresses the gap directly.
//
// Design:
//   - Scans assistant text response for assumption/hedging language
//   - HIGH confidence: explicit assumption language ("I assume", etc.)
//   - MEDIUM confidence: hedging language ("probably", "I think", etc.)
//   - Threshold: 3+ assumptions → inject guidance to verify before
//     proceeding
//   - Zero LLM cost - pure deterministic text pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// assumptionWarnThreshold: at this count, inject guidance.
	assumptionWarnThreshold = 3

	// assumptionMaxWarnings: max warnings per run.
	assumptionMaxWarnings = 2

	// assumptionMaxExamples: max assumption examples to include in hint.
	assumptionMaxExamples = 4
)

// assumptionPattern represents a detected assumption language pattern.
type assumptionPattern struct {
	level   string // "HIGH" or "MEDIUM"
	pattern *regexp.Regexp
}

// Precompiled patterns for performance. Case-insensitive.
var assumptionPatterns = []assumptionPattern{
	// HIGH confidence - explicit assumption language
	{"HIGH", regexp.MustCompile(`(?i)\bI assume\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bI'm assuming\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bI'll assume\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bI will assume\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bassuming that\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bassuming the\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bassuming you\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bassuming this\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bpresumably\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bwithout more info`)},
	{"HIGH", regexp.MustCompile(`(?i)\bin the absence of\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bwithout knowing\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bI don't know the\b.*\bbut I(?:'ll| will)?\b`)},
	// MEDIUM confidence - hedging/uncertainty language
	{"MEDIUM", regexp.MustCompile(`(?i)\bI think\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bprobably\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\blikely\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bmy best guess\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bif I had to guess\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bit seems like\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bappears to be\b`)},
	// Avoid false positives: only flag "I believe" when followed by
	// decision-oriented words, not when expressing understanding.
	{"MEDIUM", regexp.MustCompile(`(?i)\bI believe (?:this|that|the|it)\b`)},
}

// assumptionHit records a single detected assumption.
type assumptionHit struct {
	level   string
	excerpt string
}

// assumptionTrackerState tracks assumption detections across a run.
type assumptionTrackerState struct {
	warnings int
}

func newAssumptionTrackerState() *assumptionTrackerState {
	return &assumptionTrackerState{}
}

func (s *assumptionTrackerState) reset() {
	s.warnings = 0
}

// scanAssumptions analyzes assistant text for assumption language.
// Returns the list of detected assumption hits (sorted HIGH first).
func scanAssumptions(text string) []assumptionHit {
	if len(text) == 0 {
		return nil
	}

	var hits []assumptionHit
	seen := make(map[string]bool) // deduplicate by excerpt

	for _, ap := range assumptionPatterns {
		locs := ap.pattern.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			start := loc[0]
			// Extract a short excerpt around the match for context.
			excerptStart := start - 20
			if excerptStart < 0 {
				excerptStart = 0
			}
			excerptEnd := loc[1] + 40
			if excerptEnd > len(text) {
				excerptEnd = len(text)
			}
			excerpt := strings.TrimSpace(text[excerptStart:excerptEnd])
			// Truncate long excerpts.
			if len(excerpt) > 80 {
				excerpt = excerpt[:80] + "..."
			}

			// Deduplicate by excerpt content.
			key := ap.level + ":" + excerpt
			if seen[key] {
				continue
			}
			seen[key] = true

			hits = append(hits, assumptionHit{
				level:   ap.level,
				excerpt: excerpt,
			})
		}
	}

	return hits
}

// maybeWarnAssumptions checks assistant text for assumption language
// and returns a guidance message if the threshold is exceeded.
// Returns empty string if no warning is needed.
func (a *Agent) maybeWarnAssumptions(text string) string {
	if a.assumptionTracker == nil {
		return ""
	}
	if a.assumptionTracker.warnings >= assumptionMaxWarnings {
		return ""
	}

	hits := scanAssumptions(text)
	if len(hits) < assumptionWarnThreshold {
		return ""
	}

	// Count by level.
	highCount := 0
	for _, h := range hits {
		if h.level == "HIGH" {
			highCount++
		}
	}

	a.assumptionTracker.warnings++

	// Build examples list (prioritize HIGH).
	var examples []string
	for _, h := range hits {
		if len(examples) >= assumptionMaxExamples {
			break
		}
		examples = append(examples, fmt.Sprintf("  [%s] ...%s...", h.level, h.excerpt))
	}

	severity := "INFO"
	if highCount >= 2 {
		severity = "WARNING"
	}

	return fmt.Sprintf(
		"[%s-assumption] %d unverified assumption(s) (%d HIGH, %d MEDIUM). Verify before acting.\n%s",
		severity, len(hits), highCount, len(hits)-highCount,
		strings.Join(examples, "\n"),
	)
}
