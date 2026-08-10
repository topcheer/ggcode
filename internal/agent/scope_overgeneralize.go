package agent

// Scope Overgeneralization Detector -- Epistemic Calibration for Narrow Evidence
//
// Research basis:
//   - "When Planning Fails Despite Correct Execution: On Epistemic Calibration
//     for LLM-Based Multi-Agent Systems" (arXiv:2605.23414v1, May 2026):
//     Identifies epistemic miscalibration as a latent failure mode where agents
//     correctly execute actions but misjudge their knowledge state. Plans remain
//     self-consistent during planning, but the agent over-generalizes from partial
//     information, treating narrow evidence as universal truth.
//   - The paper's key insight: this failure is DYNAMIC -- new information can
//     alter feasibility assessments. The agent doesn't recognize the gap between
//     what it searched and what it claims to know.
//
// THE SCOPE INFERENCE PROBLEM:
// When an agent performs a narrow search (e.g., grep in one directory, read one
// file, glob a specific pattern), it often extrapolates from that narrow result
// to make universal claims about the entire codebase:
//
//   "There are no other callers of this function"  (after grepping one dir)
//   "This pattern isn't used anywhere"  (after a limited glob)
//   "Only these two files are affected"  (after reading just those files)
//   "All references have been updated"  (after editing the ones found in one search)
//
// This is the epistemic leap: narrow evidence -> universal quantifier. The agent's
// knowledge state is much narrower than its stated confidence. The plan remains
// executable (it CAN make the edit), but the plan's foundation (the scope claim)
// is miscalibrated.
//
// DETECTION APPROACH:
//   1. Track evidence-gathering tool calls (grep, search_files, glob, read_file)
//      and their scope indicators (single path vs broad pattern)
//   2. Detect universal-quantifier language in subsequent assistant text:
//      "nowhere", "anywhere", "no other", "all X", "only", "every", "the only"
//   3. When universal claims follow narrow evidence without a subsequent
//      verification step, warn the agent about the scope gap
//
// This is DISTINCT from:
//   - evidence_overconfidence.go: tracks evidence→edit cascade (certainty FROM
//     evidence). This detector tracks scope generalization (narrow→universal).
//   - assumption_track.go: detects hedging language. This detects the opposite:
//     unfounded universality (overconfidence in scope, not in certainty).
//   - cross_file_impact.go: static analysis of import relationships. This is
//     behavioral: what the agent CLAIMS vs what it SEARCHED.
//
// Design:
//   - Tracks narrow vs broad evidence tool calls in a sliding window
//   - Detects universal quantifier phrases in assistant text
//   - Fires when universal claims follow narrow evidence (scope gap)
//   - Verification tools (build/test) reset the state
//   - Zero LLM cost -- pure pattern matching
//   - Max 2 warnings per run, resets each user turn

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// scopeOvergenMaxWarnings: max warnings per run.
	scopeOvergenMaxWarnings = 2

	// scopeOvergenWindow: max tool calls between evidence and claim that still
	// counts as a scope-inference event.
	scopeOvergenWindow = 8

	// scopeOvergenMinNarrowEvidence: minimum narrow evidence calls to establish
	// a pattern worth checking (avoid single-read false positives).
	scopeOvergenMinNarrowEvidence = 1
)

// scopeEvidenceToolRe matches tools that gather information with potentially
// narrow scope. The agent may generalize from these to universal claims.
var scopeEvidenceToolRe = regexp.MustCompile(`(?i)\b(grep|search_files|glob|read_file|list_directory|code_search)\b`)

// scopeBroadEvidenceRe matches evidence tool calls that ARE broad enough to
// support universal claims (recursive globs, repo-wide searches).
var scopeBroadEvidenceRe = regexp.MustCompile(`(?i)\*\*|/\.\.\.|/[A-Za-z0-9_-]+/[A-Za-z0-9_-]+/|^/`)

// scopeLSPRe matches LSP tools that provide definitive cross-references.
// These are legitimate sources of universal claims (e.g., lsp_references).
var scopeLSPRe = regexp.MustCompile(`(?i)\blsp_(references|workspace_symbols|implementation|incoming_calls|outgoing_calls)\b`)

// scopeVerifyRe matches tools that provide deterministic feedback.
var scopeVerifyRe = regexp.MustCompile(`(?i)\b(run_command|start_command)\b`)

// universalClaimRe detects universal-quantifier language that generalizes from
// evidence to the entire codebase or all instances.
var universalClaimRe = []*regexp.Regexp{
	// Absence claims (no X anywhere)
	regexp.MustCompile(`(?i)\b(no|not|none|n't)\s+(other|more|additional|remaining)\b`),
	regexp.MustCompile(`(?i)\b(there (is|are) no|doesn't exist|don't exist|cannot find)\b`),
	regexp.MustCompile(`(?i)\b(nowhere|anywhere)\b`),

	// Exhaustive claims (all/only/every)
	regexp.MustCompile(`(?i)\b(all|every|each)\s+(the\s+)?(file|function|method|reference|caller|usage|import|implementation|occurrence|instance)s?\b`),
	regexp.MustCompile(`(?i)\b(the\s+)?(only|sole|single)\s+(file|files|place|places|location|locations|instance|instances|reference|references|caller|callers)\b`),
	regexp.MustCompile(`(?i)\b(just|only)\s+(these|the|those)\s+\d+\s+(file|function|method|reference|caller|location)s?\b`),

	// Completion claims (updated/fixed/handled all)
	regexp.MustCompile(`(?i)\b(all|every)\s+(reference|caller|usage|import|occurrence)s?\s+(have been|are|were)\s+(updated|fixed|handled|changed|modified|replaced)\b`),
	regexp.MustCompile(`(?i)\b(nothing (else|more|other)\s+(need|need to be|needs to be|was|is)\s+(done|changed|updated|modified))\b`),

	// Exclusivity claims
	regexp.MustCompile(`(?i)\b(this is the (only|sole|last|final))\b`),
}

// scopeOvergeneralizeState tracks the narrow-evidence → universal-claim pattern.
type scopeOvergeneralizeState struct {
	warnings            int
	narrowEvidenceCalls int  // narrow-scope evidence tool calls in current window
	broadEvidenceCalls  int  // broad-scope evidence tool calls
	lspEvidenceCalls    int  // LSP-based evidence (definitive)
	hasRecentNarrow     bool // narrow evidence called recently (within window)
	recentTools         []toolScopeEntry
}

type toolScopeEntry struct {
	name     string
	isNarrow bool
	isLSP    bool
}

func newScopeOvergeneralizeState() *scopeOvergeneralizeState {
	return &scopeOvergeneralizeState{
		recentTools: make([]toolScopeEntry, 0, scopeOvergenWindow+2),
	}
}

func (s *scopeOvergeneralizeState) reset() {
	s.warnings = 0
	s.narrowEvidenceCalls = 0
	s.broadEvidenceCalls = 0
	s.lspEvidenceCalls = 0
	s.hasRecentNarrow = false
	s.recentTools = s.recentTools[:0]
}

// recordToolCall tracks evidence tool calls and classifies their scope.
func (s *scopeOvergeneralizeState) recordToolCall(toolName, toolInput string) {
	// Sliding window of recent tool calls
	entry := toolScopeEntry{name: toolName}
	entry.isLSP = scopeLSPRe.MatchString(toolName)

	if entry.isLSP {
		s.lspEvidenceCalls++
		s.hasRecentNarrow = false // LSP evidence is definitive
		s.recentTools = append(s.recentTools, entry)
		s.trimWindow()
		return
	}

	if scopeEvidenceToolRe.MatchString(toolName) {
		// Classify as narrow or broad based on input pattern
		isBroad := scopeBroadEvidenceRe.MatchString(toolInput)
		if isBroad {
			s.broadEvidenceCalls++
		} else {
			s.narrowEvidenceCalls++
			s.hasRecentNarrow = true
		}
		entry.isNarrow = !isBroad
		s.recentTools = append(s.recentTools, entry)
		s.trimWindow()
		return
	}

	// Verification tools clear narrow evidence state
	if scopeVerifyRe.MatchString(toolName) || verificationToolRe.MatchString(toolInput) {
		s.hasRecentNarrow = false
	}

	// Record non-evidence tools too for window tracking
	s.recentTools = append(s.recentTools, entry)
	s.trimWindow()
}

func (s *scopeOvergeneralizeState) trimWindow() {
	if len(s.recentTools) > scopeOvergenWindow {
		s.recentTools = s.recentTools[1:]
		// Recompute hasRecentNarrow from the trimmed window
		s.hasRecentNarrow = false
		for _, e := range s.recentTools {
			if e.isNarrow {
				s.hasRecentNarrow = true
				break
			}
		}
	}
}

// maybeWarnScopeOvergeneralize checks if the agent is making universal claims
// based on narrow evidence and returns guidance if so.
func (a *Agent) maybeWarnScopeOvergeneralize(assistantText string) string {
	s := a.scopeOvergeneralize
	if s == nil || !s.hasRecentNarrow || s.narrowEvidenceCalls < scopeOvergenMinNarrowEvidence {
		return ""
	}

	// LSP-based evidence makes universal claims legitimate
	if s.lspEvidenceCalls > 0 || hasBroadEvidenceInWindow(s) {
		return ""
	}

	matchedPhrases := findUniversalClaims(assistantText)
	if len(matchedPhrases) == 0 || s.warnings >= scopeOvergenMaxWarnings {
		return ""
	}

	s.warnings++
	return buildScopeOvergenMessage(s, matchedPhrases[0])
}

// findUniversalClaims searches text for universal-quantifier phrases,
// returning up to 2 matched snippets.
func findUniversalClaims(text string) []string {
	matched := make([]string, 0, 2)
	for _, re := range universalClaimRe {
		loc := re.FindStringIndex(text)
		if loc == nil {
			continue
		}
		if len(matched) < 2 {
			start := loc[0] - 30
			if start < 0 {
				start = 0
			}
			end := loc[1] + 30
			if end > len(text) {
				end = len(text)
			}
			matched = append(matched, strings.TrimSpace(text[start:end]))
		}
	}
	return matched
}

// hasBroadEvidenceInWindow checks if any broad-scope evidence tool was used.
func hasBroadEvidenceInWindow(s *scopeOvergeneralizeState) bool {
	for _, e := range s.recentTools {
		if !e.isNarrow && !e.isLSP && scopeEvidenceToolRe.MatchString(e.name) {
			return true
		}
	}
	return false
}

// buildScopeOvergenMessage constructs the advisory guidance string.
func buildScopeOvergenMessage(s *scopeOvergeneralizeState, firstClaim string) string {
	return fmt.Sprintf(
		"[scope-overgeneralization] Universal claim after narrow search (%d narrow, %d broad). Claim: \"%s\". Use recursive glob or lsp_references before claiming codebase-wide.",
		s.narrowEvidenceCalls, s.broadEvidenceCalls, firstClaim,
	)
}
