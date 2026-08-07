package agent

// Selective Evidence Detector -- Confirmation Bias in Agent Reasoning
//
// Research basis:
//   - "Measuring and Exploiting Confirmation Bias in LLM-Assisted Security
//     Analysis" (arXiv 2603.18740, 2026) demonstrates that LLM agents
//     systematically cherry-pick evidence confirming their prior hypothesis
//     while dismissing or rationalizing away contradictory signals.
//   - "Agent Sycophancy and Confirmation Bias: Defence Patterns for Codex CLI"
//     (2026) shows that reasoning models can build elaborate post-hoc
//     rationalizations, creating a false sense of objectivity while
//     selectively processing evidence.
//   - AgentForesight (arXiv 2605.08715, 2025) frames early failure detection
//     as "online auditing" -- catching decisive errors before the trajectory
//     completes. Confirmation bias is a leading cause of missed early signals.
//
// Problem: When an agent has formed a hypothesis about root cause or solution,
// it tends to:
//   1. Quote evidence that confirms its hypothesis ("the test passes", "this
//      is correct", "as expected") -- even cherry-picking from partial output
//   2. Dismiss or minimize contradicting evidence ("this error is unrelated",
//      "can be safely ignored", "false positive", "not relevant to this")
//   3. Declare success prematurely while unresolved errors linger
//
// This is NOT the same as:
//   - unverified_claim.go: detects claiming success without running verification
//   - assumption_track.go: detects forward-looking unverified guesses
//   - circular_reason.go: detects tautological justification
//   - THIS: detects the pattern of selectively emphasizing supportive evidence
//     while dismissing contradicting evidence -- the hallmark of confirmation bias
//
// Detection approach (zero LLM cost):
//   Track positive-evidence claims AND dismissive language about negatives.
//   When both are present in the same response (agent celebrates successes
//   while hand-waving away errors/warnings), inject guidance to re-examine
//   the dismissed evidence with fresh eyes.
//
// Interaction with existing detectors:
//   - Complements unverified_claim by catching the inverse: not just claiming
//     success, but actively suppressing contradicting signals
//   - Complements assumption_track by detecting backward-looking rationalization
//     (explaining away evidence) vs. forward-looking guessing

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// selectiveEvidenceWarnThreshold: minimum combined score (positive claims
	// + dismissive statements) needed to trigger a warning.
	selectiveEvidenceWarnThreshold = 3

	// selectiveEvidenceMaxWarnings: cap warnings per run to avoid nagging.
	selectiveEvidenceMaxWarnings = 2

	// selectiveEvidenceMaxExamples: max examples shown in guidance.
	selectiveEvidenceMaxExamples = 4

	// selectiveEvidenceTextLimit: max chars of assistant text to scan.
	selectiveEvidenceTextLimit = 12000
)

// selectiveEvidenceTrackerState tracks warnings issued during a run.
type selectiveEvidenceTrackerState struct {
	warnings int
}

func newSelectiveEvidenceTrackerState() *selectiveEvidenceTrackerState {
	return &selectiveEvidenceTrackerState{}
}

func (s *selectiveEvidenceTrackerState) reset() {
	s.warnings = 0
}

// evidenceClaim represents a detected claim in assistant text.
type evidenceClaim struct {
	category string // "positive" or "dismissive"
	excerpt  string
}

// Precompiled patterns. Case-insensitive.
//
// Positive-evidence patterns: agent emphasizes confirming evidence.
var positiveEvidencePatterns = []*regexp.Regexp{
	// Success declarations
	regexp.MustCompile(`(?i)(?:this|the code|the test[s]?|the build|the output)\s+(?:works|passes|is correct|is working|succeeded|completed successfully)`),
	regexp.MustCompile(`(?i)(?:verified|confirmed)\s+(?:that|this|the)`),
	regexp.MustCompile(`(?i)(?:as expected|behaving correctly|functioning properly|working as intended)`),
	regexp.MustCompile(`(?i)(?:test[s]?\s+)?pass(?:ed|ing)?\s+(?:successfully|without issue|as expected)`),
	regexp.MustCompile(`(?i)(?:good|great|excellent)\s+(?:news|sign|indicator)`),
	regexp.MustCompile(`(?i)(?:correctly|properly|accurately)\s+(?:handles?|processes?|implements?|resolves?)`),
	// Selective quotation from output -- "notice the X" implying only X matters
	regexp.MustCompile(`(?i)(?:notice|see|observe|note)\s+(?:that|how|the)\s+.{0,40}(?:success|correct|pass|working|valid)`),
}

// Dismissive-evidence patterns: agent waves away contradicting signals.
var dismissiveEvidencePatterns = []*regexp.Regexp{
	// Direct dismissal
	regexp.MustCompile(`(?i)(?:can be|is|are)\s+(?:safely\s+)?ignored`),
	regexp.MustCompile(`(?i)(?:not\s+)?(?:relevant|related|connected)\s+to\s+(?:this|the current|our)`),
	regexp.MustCompile(`(?i)(?:false positive|pre-existing|unrelated (?:issue|error|warning))`),
	regexp.MustCompile(`(?i)don'?t (?:worry|need to worry) about`),
	regexp.MustCompile(`(?i)no(?:t a)?(?:thing)? to (?:worry|concern) (?:about|us)`),
	regexp.MustCompile(`(?i)(?:expected|known|normal)\s+(?:behavior|behaviour|error|warning|failure)`),
	// Rationalization patterns
	regexp.MustCompile(`(?i)happens because\s+.{0,30}(?:not actually|different from|separate from)`),
	regexp.MustCompile(`(?i)(?:despite|although|even though).{0,50}(?:not a problem|fine|okay|acceptable)`),
	// Minimizing language
	regexp.MustCompile(`(?i)(?:just|merely|simply)\s+(?:a (?:minor|cosmetic|benign)|an? (?:unrelated|separate))`),
	regexp.MustCompile(`(?i)(?:doesn'?t|does not)\s+(?:actually\s+)?(?:affect|impact|matter|break)`),
}

// scanEvidenceClaims analyzes assistant text for selective evidence processing.
func scanEvidenceClaims(text string) []evidenceClaim {
	if len(text) == 0 {
		return nil
	}
	// Limit scan length for performance.
	if len(text) > selectiveEvidenceTextLimit {
		text = text[:selectiveEvidenceTextLimit]
	}

	var claims []evidenceClaim
	seen := make(map[string]bool)

	extractExcerpt := func(text string, matchIdx []int) string {
		start := matchIdx[0] - 15
		if start < 0 {
			start = 0
		}
		end := matchIdx[1] + 35
		if end > len(text) {
			end = len(text)
		}
		excerpt := strings.TrimSpace(text[start:end])
		if len(excerpt) > 70 {
			excerpt = excerpt[:70] + "..."
		}
		return excerpt
	}

	for _, p := range positiveEvidencePatterns {
		for _, idx := range p.FindAllStringIndex(text, -1) {
			excerpt := extractExcerpt(text, idx)
			key := "pos:" + excerpt
			if !seen[key] {
				seen[key] = true
				claims = append(claims, evidenceClaim{category: "positive", excerpt: excerpt})
			}
		}
	}

	for _, p := range dismissiveEvidencePatterns {
		for _, idx := range p.FindAllStringIndex(text, -1) {
			excerpt := extractExcerpt(text, idx)
			key := "dis:" + excerpt
			if !seen[key] {
				seen[key] = true
				claims = append(claims, evidenceClaim{category: "dismissive", excerpt: excerpt})
			}
		}
	}

	return claims
}

// maybeWarnSelectiveEvidence detects confirmation bias patterns in assistant
// text. Fires when the agent simultaneously emphasizes positive evidence AND
// dismisses negative evidence -- the hallmark of selective evidence processing.
func (a *Agent) maybeWarnSelectiveEvidence(text string) string {
	if a.selectiveEvidence == nil {
		return ""
	}
	if a.selectiveEvidence.warnings >= selectiveEvidenceMaxWarnings {
		return ""
	}

	claims := scanEvidenceClaims(text)
	if len(claims) < selectiveEvidenceWarnThreshold {
		return ""
	}

	// Count by category.
	posCount := 0
	disCount := 0
	for _, c := range claims {
		switch c.category {
		case "positive":
			posCount++
		case "dismissive":
			disCount++
		}
	}

	// Only warn when BOTH categories are present -- this is the selective
	// evidence pattern. Having only positive claims is normal success reporting;
	// having only dismissive claims is normal triage.
	if posCount == 0 || disCount == 0 {
		return ""
	}

	a.selectiveEvidence.warnings++

	// Build examples list.
	var posExamples, disExamples []string
	for _, c := range claims {
		switch c.category {
		case "positive":
			if len(posExamples) < selectiveEvidenceMaxExamples/2 {
				posExamples = append(posExamples, "  [+] ..."+c.excerpt+"...")
			}
		case "dismissive":
			if len(disExamples) < selectiveEvidenceMaxExamples/2 {
				disExamples = append(disExamples, "  [-] ..."+c.excerpt+"...")
			}
		}
	}

	return fmt.Sprintf(
		"[confirmation-bias] Detected selective evidence processing: %d positive claim(s) "+
			"alongside %d dismissive statement(s) about negative evidence. "+
			"You may be cherry-picking evidence that confirms your hypothesis while "+
			"rationalizing away contradictory signals. This is a known failure mode "+
			"in AI coding agents (confirmation bias). "+
			"Re-examine each dismissed error/warning independently -- verify it is "+
			"genuinely unrelated rather than assuming it. "+
			"If any dismissed item could plausibly be connected to the current issue, "+
			"investigate it before declaring success.\n"+
			"Positive claims:\n%s\n"+
			"Dismissed evidence:\n%s",
		posCount, disCount,
		strings.Join(posExamples, "\n"),
		strings.Join(disExamples, "\n"),
	)
}
