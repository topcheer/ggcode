package agent

// Satisficing Settling Detector
//
// Research basis:
//   - "Bounded Rationality for LLMs: Satisficing Alignment at Inference-Time"
//     (Chehade et al., arXiv:2505.23729, ICML 2025): Satisficing strategies
//     optimize primary objectives while ensuring others meet acceptable
//     thresholds. When LLM agents apply this unconsciously, they knowingly
//     accept suboptimal or incomplete solutions ("good enough for now",
//     "temporary fix", "workaround") without a concrete plan to address
//     the gap later.
//   - Presenc AI "AI Agent Failure-Mode Statistics 2026": incomplete task
//     delivery -- agents declare success on partially-complete work -- is a
//     top-3 failure mode for agent pilots.
//   - Herbert Simon's satisficing theory (1956): decision-makers settle
//     for "good enough" rather than optimal. In coding agents this manifests
//     as knowingly incomplete solutions: "this isn't ideal but it works",
//     "temporary workaround", "we can revisit later".
//
// Problem: AI coding agents frequently settle for suboptimal solutions
// while explicitly acknowledging the gap:
//
//  1. "This is a temporary fix, we should revisit it later" -- no tracking
//  2. "It's not ideal but it works for now" -- knowingly degrading quality
//  3. "This is a quick workaround" -- shortcut without follow-up plan
//  4. "Good enough for the current use case" -- lowering the bar
//  5. "Let's just use a simple approach for now" -- premature simplicity
//
// Unlike premature surrender (which gives up entirely), satisficing
// settling delivers a solution the agent KNOWS is incomplete. The danger
// is twofold: (a) the "temporary" solution often becomes permanent, and
// (b) the user may not realize the solution is knowingly suboptimal.
//
// Distinct from existing detectors:
//   - assumption_track: unverified GUESSES (agent doesn't know the answer)
//   - premature_surrender: agent GIVES UP (no solution delivered)
//   - deferred_work: tracks explicitly deferred subtasks via "later/I'll"
//   - THIS detector: agent DELIVERS but KNOWS it's incomplete/suboptimal
//
// The satisficing-settling pattern is: [solution delivered] + [explicit
// acknowledgment that it's suboptimal/temporary/workaround].

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	satisficingMaxWarnings = 2 // cap warnings per run
	satisficingThreshold   = 2 // min hits to trigger
	satisficingMaxExamples = 4
)

// satisficingPattern defines a settlement language pattern.
type satisficingPattern struct {
	level   string
	pattern *regexp.Regexp
}

// satisficingPatterns detects explicit acknowledgment of suboptimal solutions.
var satisficingPatterns = []satisficingPattern{
	// HIGH: explicit "temporary"/"temporarily" with a solution context
	{"HIGH", regexp.MustCompile(`(?i)\btemporar(?:y|ily)\b.*\b(?:fix|solution|approach|workaround|measure|hack|patch)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\b(?:fix|solution|approach|workaround|measure|hack|patch)\b.*\btemporar(?:y|ily)\b`)},
	// HIGH: "not ideal but" -- knowingly degrading
	{"HIGH", regexp.MustCompile(`(?i)\bnot (?:ideal|perfect|great|optimal|the best)\b.*\bbut\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bit'?ll do\b|\bthis (?:will|should) (?:do|suffice)\b|\bclose enough\b`)},
	// MEDIUM: "for now" / "for the time being" -- deferring quality
	{"MEDIUM", regexp.MustCompile(`(?i)\bfor now\b.*\b(?:work|suffic|do|enough)\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bfor the time being\b`)},
	// MEDIUM: "workaround" / "quick fix" / "quick and dirty"
	{"MEDIUM", regexp.MustCompile(`(?i)\bquick (?:fix|and dirty|workaround|hack)\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bworkaround\b`)},
	// MEDIUM: "good enough" / "acceptable for now"
	{"MEDIUM", regexp.MustCompile(`(?i)\bgood enough\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bacceptabl[ei]\b.*\b(?:for now|for now)\b`)},
	// MEDIUM: "should be fine" / "should work" (hedged solution delivery)
	{"MEDIUM", regexp.MustCompile(`(?i)\bshould (?:be fine|work for now|suffice)\b`)},
	// LOW: "simpler approach" / "basic version" -- may be legitimate
	{"LOW", regexp.MustCompile(`(?i)\bsimpler?\s+approach\b|\bbasic version\b`)},
	{"LOW", regexp.MustCompile(`(?i)\bwe can (?:improve|revisit|enhance|refine) (?:this|it) later\b`)},
}

// satisficingHit records a single settlement acknowledgment.
type satisficingHit struct {
	level   string
	excerpt string
}

// satisficingSettleState tracks settlement detections across a run.
type satisficingSettleState struct {
	warnings int
}

func newSatisficingSettleState() *satisficingSettleState {
	return &satisficingSettleState{}
}

func (s *satisficingSettleState) reset() {
	s.warnings = 0
}

// scanSatisficing analyzes assistant text for settlement language.
// Returns the list of detected settlement hits (sorted HIGH first).
func scanSatisficing(text string) []satisficingHit {
	if len(text) == 0 {
		return nil
	}

	var hits []satisficingHit
	seen := make(map[string]bool) // deduplicate by excerpt

	for _, sp := range satisficingPatterns {
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

			key := sp.level + ":" + excerpt
			if seen[key] {
				continue
			}
			seen[key] = true

			hits = append(hits, satisficingHit{
				level:   sp.level,
				excerpt: excerpt,
			})
		}
	}

	return hits
}

// maybeWarnSatisficing checks assistant text for settlement language
// and returns a guidance message if the threshold is exceeded.
// Returns empty string if no warning is needed.
func (a *Agent) maybeWarnSatisficing(text string) string {
	if a.satisficingSettle == nil {
		return ""
	}
	if a.satisficingSettle.warnings >= satisficingMaxWarnings {
		return ""
	}

	hits := scanSatisficing(text)
	if len(hits) < satisficingThreshold {
		return ""
	}

	// Count by level.
	highCount := 0
	for _, h := range hits {
		if h.level == "HIGH" {
			highCount++
		}
	}

	a.satisficingSettle.warnings++

	// Build examples list (prioritize HIGH).
	var examples []string
	for _, h := range hits {
		if len(examples) >= satisficingMaxExamples {
			break
		}
		if h.level == "LOW" && len(examples) >= 2 {
			continue // skip LOW if we already have examples
		}
		examples = append(examples, fmt.Sprintf("  [%s] ...%s...", h.level, h.excerpt))
	}

	severity := "INFO"
	if highCount >= 2 {
		severity = "WARNING"
	}

	return fmt.Sprintf(
		"[%s-satisficing] Detected %d instance(s) where you acknowledged the "+
			"solution is suboptimal, temporary, or incomplete "+
			"(%d HIGH, %d MEDIUM/LOW). "+
			"Satisficing settling -- delivering a knowingly incomplete solution "+
			"without a concrete follow-up plan -- is a leading failure mode. "+
			"The 'temporary' fix often becomes permanent, and the user may not "+
			"realize the quality bar was knowingly lowered.\n"+
			"Before finalizing: either (a) address the gap now if it's within "+
			"scope, or (b) explicitly document the limitation with a concrete "+
			"TODO/FIXME and state what was deferred and why.\n"+
			"Detected settlement language:\n%s",
		severity, len(hits), highCount, len(hits)-highCount,
		strings.Join(examples, "\n"),
	)
}
