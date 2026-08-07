package agent

// Evidence-Induced Overconfidence Detector -- Tool-Type Calibration Asymmetry
//
// Research basis:
//   - Zhang et al., "Agentic Confidence Calibration" (arXiv:2601.15778, Jan 2026):
//     HTC framework identifies "uncertainty from external tools" as a core
//     agentic calibration challenge. Tool calls introduce epistemic uncertainty
//     that agents fail to propagate through their reasoning.
//   - AgentMarketCap calibration analysis (Apr 2026): "Evidence tools (web
//     search, document retrieval) systematically induce SEVERE overconfidence
//     due to inherent noise in retrieved information. The agent treats noisy
//     evidence as authoritative confirmation."
//     vs. "Verification tools (code interpreters, calculators) can MITIGATE
//     miscalibration through deterministic feedback."
//   - Multi-step calibration cascades: "A 10% miscalibration rate per step
//     becomes a 35% cumulative error rate over four sequential steps."
//   - CogCal-1 (2026): Overconfidence scales with difficulty; bidirectional
//     miscalibration exists (both over- and under-confidence).
//
// THE EVIDENCE ILLUSION PROBLEM:
// When an agent calls evidence-gathering tools (web_search, web_fetch, grep,
// read_file), the retrieved information creates a false sense of certainty.
// The agent treats the search result as ground truth and proceeds to make
// definitive claims or code edits without recognizing that:
//   1. Web search results may be outdated, incorrect, or contradictory
//   2. grep matches may be incomplete or misleading without full context
//   3. Read file content is a snapshot, not the complete picture
//   4. Each unverified evidence step compounds calibration error
//
// This is DISTINCT from unverified_confidence.go (which checks whether
// build/test ran after code edits) and assumption_track.go (which detects
// hedging language). This detector targets the inverse: certainty that
// emerges FROM evidence tools without adequate cross-verification.
//
// Design:
//   - Tracks evidence tool call sequences and their staleness
//   - Detects definitive claims derived from evidence ("the docs say",
//     "based on my search", "this confirms", "I found the answer")
//   - Detects code edits that immediately follow evidence tools without
//     any verification step (build/test) in between -- the evidence cascade
//   - Zero LLM cost -- pure pattern matching + tool call sequence tracking
//   - Fires at most 2 times per run (advisory, non-blocking)
//   - Resets evidence state when verification tools are called

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// evidenceOverconfMaxWarnings: max warnings per run.
	evidenceOverconfMaxWarnings = 2

	// evidenceOverconfCascadeWindow: max tool calls between evidence and edit
	// that still counts as a "cascade" (evidence → edit without verification).
	evidenceOverconfCascadeWindow = 5

	// evidenceOverconfMinEvidenceCalls: minimum evidence calls before the
	// detector considers the pattern established (avoid single-read noise).
	evidenceOverconfMinEvidenceCalls = 2
)

// evidenceToolRe matches tool names that gather information (not verify).
// These tools introduce uncertainty that agents often fail to propagate.
var evidenceToolRe = regexp.MustCompile(`(?i)\b(web_search|web_fetch|search_files|grep|glob|code_search)\b|(?i)\blsp_`)

// verificationEvidenceToolRe matches tools that provide deterministic feedback.
// These MITIGATE calibration error per the research.
var verificationEvidenceToolRe = regexp.MustCompile(`(?i)\b(run_command|start_command)\b`)

// evidenceDerivedClaimRe detects definitive assertions that attribute
// certainty to evidence-gathering activities.
var evidenceDerivedClaimRe = []*regexp.Regexp{
	// Attribution to external sources
	regexp.MustCompile(`(?i)\b(the docs?|documentation)\s+(say|state|confirm|indicate|show)`),
	regexp.MustCompile(`(?i)\b(according to (the )?(docs|documentation|search|results?|reference))`),
	regexp.MustCompile(`(?i)\b(based on my (search|research|findings?|investigation))`),
	regexp.MustCompile(`(?i)\b(the search results? (show|confirm|indicate|reveal))`),
	regexp.MustCompile(`(?i)\b(this (confirms|verifies|validates) (that|my|the))`),
	// Discovery certainty
	regexp.MustCompile(`(?i)\bI (found (the )?(answer|solution|correct|exact|right))`),
	regexp.MustCompile(`(?i)\b(I've (confirmed|verified) that)\b`),
	regexp.MustCompile(`(?i)\b(the correct (way|approach|method) (is|to))`),
	regexp.MustCompile(`(?i)\b(this is (the )?right (way|approach))`),
	// Authoritative assertion from search
	regexp.MustCompile(`(?i)\b(clearly|obviously|definitely) (the |a )`),
	regexp.MustCompile(`(?i)\b(as (expected|confirmed|documented))`),
}

// evidenceOverconfidenceState tracks the evidence→certainty calibration pattern.
type evidenceOverconfidenceState struct {
	warnings      int
	evidenceCalls int  // total evidence tool calls this run
	hasRecentEvid bool // evidence called recently (within cascade window)
	recentTools   []string
	editAfterEvid bool // edit followed evidence without verification
}

func newEvidenceOverconfidenceState() *evidenceOverconfidenceState {
	return &evidenceOverconfidenceState{
		recentTools: make([]string, 0, evidenceOverconfCascadeWindow+2),
	}
}

func (s *evidenceOverconfidenceState) reset() {
	s.warnings = 0
	s.evidenceCalls = 0
	s.hasRecentEvid = false
	s.editAfterEvid = false
	s.recentTools = s.recentTools[:0]
}

// recordToolCall tracks evidence, edit, and verification tool sequences.
func (s *evidenceOverconfidenceState) recordToolCall(toolName, toolInput string) {
	// Track recent tool calls (sliding window)
	s.recentTools = append(s.recentTools, toolName)
	if len(s.recentTools) > evidenceOverconfCascadeWindow+2 {
		s.recentTools = s.recentTools[1:]
	}

	if evidenceToolRe.MatchString(toolName) {
		s.evidenceCalls++
		s.hasRecentEvid = true
		return
	}

	// Verification tools clear the evidence state -- they provide deterministic
	// feedback that mitigates the calibration error from evidence tools.
	if isVerificationToolCall(toolName, toolInput) {
		s.hasRecentEvid = false
		s.editAfterEvid = false
		return
	}

	// Code edit after evidence without verification = calibration cascade
	if s.hasRecentEvid && isCodeEditTool(toolName) {
		s.editAfterEvid = true
	}
}

// isVerificationToolCall checks if a tool call provides deterministic feedback.
func isVerificationToolCall(toolName, toolInput string) bool {
	if verificationEvidenceToolRe.MatchString(toolName) {
		return true
	}
	// run_command/start_command with build/test patterns
	if verificationToolRe.MatchString(toolInput) {
		return true
	}
	return false
}

// maybeWarnEvidenceOverconfidence checks if the agent is exhibiting
// evidence-induced overconfidence and returns guidance if so.
func (a *Agent) maybeWarnEvidenceOverconfidence(assistantText string) string {
	s := a.evidenceOverconfidence
	if s == nil {
		return ""
	}
	if s.warnings >= evidenceOverconfMaxWarnings {
		return ""
	}

	// Pattern 1: Evidence cascade -- edited code based on evidence without
	// verification. This is the compounding calibration error pattern.
	if s.editAfterEvid && s.evidenceCalls >= evidenceOverconfMinEvidenceCalls {
		s.warnings++
		s.editAfterEvid = false
		s.hasRecentEvid = false
		return fmt.Sprintf(
			"[Calibration] Code edited after %d evidence tool call(s) without "+
				"deterministic verification. Retrieved information (search, grep, read) "+
				"introduces uncertainty that compounds across steps -- a 10%% miscalibration "+
				"per step becomes ~35%% over 4 steps. Evidence tools induce false certainty; "+
				"run build/test to ground the changes before proceeding.",
			s.evidenceCalls,
		)
	}

	// Pattern 2: Definitive claims derived from evidence tools
	if s.hasRecentEvid && s.evidenceCalls >= evidenceOverconfMinEvidenceCalls {
		claims := findEvidenceDerivedClaims(assistantText)
		if len(claims) > 0 {
			s.warnings++
			s.hasRecentEvid = false
			var b strings.Builder
			b.WriteString("[Calibration] Definitive claims made after evidence-gathering ")
			b.WriteString("tools without cross-verification. Retrieved evidence (web search, ")
			b.WriteString("grep, file reads) is noisy -- treating it as ground truth induces ")
			b.WriteString("overconfidence. The research shows evidence tools make calibration ")
			b.WriteString("WORSE, not better. Cross-check before asserting certainty: ")
			for i, c := range claims {
				if i >= 3 {
					b.WriteString(", ...")
					break
				}
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(fmt.Sprintf("%q", truncateEvidenceClaim(c)))
			}
			b.WriteString(". Verify claims with build/test/lint or acknowledge residual uncertainty.")
			return b.String()
		}
	}

	return ""
}

// findEvidenceDerivedClaims extracts definitive assertions attributed to evidence.
func findEvidenceDerivedClaims(text string) []string {
	if text == "" {
		return nil
	}
	var claims []string
	for _, re := range evidenceDerivedClaimRe {
		locs := re.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			start := loc[0]
			// Extract the sentence containing the match
			sentenceStart := strings.LastIndex(text[:start], ".")
			if sentenceStart == -1 {
				sentenceStart = 0
			} else {
				sentenceStart++ // skip the period
			}
			sentenceEnd := strings.Index(text[start:], ".")
			if sentenceEnd == -1 {
				sentenceEnd = len(text) - start
			} else {
				sentenceEnd += start
			}
			sentence := strings.TrimSpace(text[sentenceStart:sentenceEnd])
			if len(sentence) > 5 {
				claims = append(claims, sentence)
			}
			if len(claims) >= 3 {
				return claims
			}
		}
	}
	return claims
}

// truncateEvidenceClaim limits claim length for display.
func truncateEvidenceClaim(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:77] + "..."
}
