package agent

// Unverified Confidence Detector -- EpiCaR-inspired Calibration Gap
//
// Research basis:
//   - EpiCaR (arXiv:2601.06786, 2025): "Knowing What You Don't Know Matters
//     for Better Reasoning" -- agents lose calibration, expressing high confidence
//     even when they haven't verified their work.
//   - Zhu et al., "Scaling Test-time Compute for LLM Agents" (arXiv:2506.12928,
//     2025): Finding #2 -- "Knowing when to reflect is important for agents."
//   - Zhang et al., "Agentic Confidence Calibration" (arXiv:2601.15778, 2026):
//     Agents exhibit "overconfidence in failure" -- they claim success without
//     checking.
//
// Problem: Coding agents frequently express overconfident assertions about
// correctness ("this will definitely work", "the fix is complete", "this
// resolves the issue") WITHOUT having actually run any verification (build,
// tests, lint). The existing assumption tracker catches hedging language
// ("I assume", "probably"), but it does NOT catch the inverse: assertions of
// certainty that lack evidence. These false-confidence statements are dangerous
// because they signal "done" to the user when work may be incorrect.
//
// This detector tracks:
//   1. Whether the agent has expressed overconfident completion claims
//   2. Whether verification tools (run_command with build/test, etc.) were
//      actually called since the last code edit
//   3. If there's a gap (confidence expressed + no verification), inject a
//      calibration reminder to run tests before claiming completion
//
// Design:
//   - Scans assistant text for overconfident language about code correctness
//   - Tracks whether build/test/lint commands were run recently
//   - Only fires when confidence claims are NOT backed by verification
//   - Zero LLM cost - pure deterministic pattern matching + tool call tracking
//   - Fires at most 2 times per run (advisory, non-blocking)
//   - Does NOT fire if user explicitly asked for no tests or verification is
//     running in background

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// unverifiedConfMaxWarnings: max warnings per run.
	unverifiedConfMaxWarnings = 2

	// unverifiedConfMaxExamples: max confidence examples in hint text.
	unverifiedConfMaxExamples = 3

	// unverifiedConfRecentWindow: number of recent tool calls to check for verification.
	unverifiedConfRecentWindow = 8
)

// verificationToolPatterns matches tool names that count as verification.
var verificationToolRe = regexp.MustCompile(`(?i)(run_command|make verify|go test|go build|go vet|npm test|npm run|yarn test|pytest|cargo test|cargo build|make test|make build|make lint|check)`)

// overconfidentPatterns represents assertions of certainty about code correctness
// that should have been verified but may not have been.
var overconfidentPatterns = []*regexp.Regexp{
	// Completion/success claims
	regexp.MustCompile(`(?i)\bthis will (definitely|certainly|surely)\b`),
	regexp.MustCompile(`(?i)\bdefinitely (work|fix|resolve)s?\b`),
	regexp.MustCompile(`(?i)\bcertainly (work|fix|resolve)s?\b`),
	regexp.MustCompile(`(?i)\bthis (correctly|fully|completely) (fix|handle|resolve|address)`),
	regexp.MustCompile(`(?i)\bthe fix is (complete|done|ready)\b`),
	regexp.MustCompile(`(?i)\b(fully|completely) (resolved|fixed|solved|addressed)\b`),
	regexp.MustCompile(`(?i)\bguaranteed to (work|fix|resolve)\b`),
	regexp.MustCompile(`(?i)\bthis ensures?\b`),
	regexp.MustCompile(`(?i)\bnow (work|fix|resolve) properly\b`),
	regexp.MustCompile(`(?i)\bwill now (work|function) correctly\b`),
	regexp.MustCompile(`(?i)\bthe (issue|bug|error) is (now )?(resolved|fixed|solved)\b`),
	regexp.MustCompile(`(?i)\bchanges? are? correct\b`),
	regexp.MustCompile(`(?i)\bworks? as expected\b`),
	regexp.MustCompile(`(?i)\bproperly (handles?|implements?|resolves?)\b`),
	regexp.MustCompile(`(?i)\bno longer (an issue|a problem|occurs?)\b`),
	// "Nothing to worry about" dismissive confidence
	regexp.MustCompile(`(?i)\bnothing (else )?to worry about\b`),
	regexp.MustCompile(`(?i)\bshould work fine\b`),
}

// confidenceClaim captures a single overconfident assertion.
type confidenceClaim struct {
	text      string // matched text snippet
	statement string // full sentence containing the claim
}

// unverifiedConfidenceState tracks confidence claims and verification status.
type unverifiedConfidenceState struct {
	warnings    int
	codeEdited  bool // true if edit/write tools were called since last verification
	verified    bool // true if verification tools were called since last code edit
	recentTools []string
}

func newUnverifiedConfidenceState() *unverifiedConfidenceState {
	return &unverifiedConfidenceState{
		recentTools: make([]string, 0, unverifiedConfRecentWindow),
	}
}

func (s *unverifiedConfidenceState) reset() {
	s.warnings = 0
	s.codeEdited = false
	s.verified = false
	s.recentTools = s.recentTools[:0]
}

// recordToolCall tracks whether a tool call counts as code editing or verification.
func (s *unverifiedConfidenceState) recordToolCall(toolName, toolInput string) {
	// Track recent tool calls (sliding window)
	s.recentTools = append(s.recentTools, toolName)
	if len(s.recentTools) > unverifiedConfRecentWindow {
		s.recentTools = s.recentTools[1:]
	}

	// Check if this is a code-editing tool
	if isCodeEditTool(toolName) {
		s.codeEdited = true
		s.verified = false // reset verification flag on new edits
		return
	}

	// Check if this is a verification tool
	if verificationToolRe.MatchString(toolName) || verificationToolRe.MatchString(toolInput) {
		s.verified = true
		s.codeEdited = false
	}
}

// isCodeEditTool returns true for tools that modify source files.
func isCodeEditTool(toolName string) bool {
	switch toolName {
	case "edit_file", "multi_edit_file", "write_file", "multi_file_edit",
		"batch_replace", "notebook_edit", "file_ops":
		return true
	default:
		return false
	}
}

// extractSentence extracts the sentence containing the given byte offset from text.
func extractSentence(text string, matchIdx []int) string {
	start := matchIdx[0]
	end := matchIdx[1]

	// Find sentence start: scan backward for . ! \n
	sentenceStart := start
	for i := start - 1; i >= 0; i-- {
		if text[i] == '.' || text[i] == '!' || text[i] == '\n' {
			sentenceStart = i + 1
			break
		}
		if i == 0 {
			sentenceStart = 0
		}
	}

	// Find sentence end: scan forward for . ! \n
	sentenceEnd := end
	for i := end; i < len(text); i++ {
		if text[i] == '.' || text[i] == '!' || text[i] == '\n' {
			sentenceEnd = i
			break
		}
		if i == len(text)-1 {
			sentenceEnd = len(text)
		}
	}

	return strings.TrimSpace(text[sentenceStart:sentenceEnd])
}

// detectOverconfidentClaims scans assistant text and returns matched confidence claims.
func detectOverconfidentClaims(text string) []confidenceClaim {
	var claims []confidenceClaim
	for _, ptn := range overconfidentPatterns {
		matches := ptn.FindAllStringIndex(text, -1)
		for _, idx := range matches {
			claim := confidenceClaim{
				text:      text[idx[0]:idx[1]],
				statement: extractSentence(text, idx),
			}
			// Avoid duplicate statements
			dup := false
			for _, existing := range claims {
				if existing.statement == claim.statement {
					dup = true
					break
				}
			}
			if !dup {
				claims = append(claims, claim)
			}
		}
	}
	return claims
}

// hasRecentVerification checks whether any recent tool calls look like verification.
func (s *unverifiedConfidenceState) hasRecentVerification() bool {
	if s.verified {
		return true
	}
	// Also check recent tool call names
	for _, tn := range s.recentTools {
		if verificationToolRe.MatchString(tn) {
			return true
		}
	}
	return false
}

// maybeWarnUnverifiedConfidence checks for overconfident claims without verification
// and returns a guidance message if calibration is needed.
func (a *Agent) maybeWarnUnverifiedConfidence(assistantText string) string {
	if a.unverifiedConfidence == nil {
		return ""
	}
	s := a.unverifiedConfidence

	// Rate limit: max warnings per run
	if s.warnings >= unverifiedConfMaxWarnings {
		return ""
	}

	claims := detectOverconfidentClaims(assistantText)
	if len(claims) == 0 {
		return ""
	}

	// If code was edited but not verified, this is the key gap
	if s.codeEdited && !s.hasRecentVerification() {
		s.warnings++

		var examples []string
		for i, c := range claims {
			if i >= unverifiedConfMaxExamples {
				break
			}
			ex := c.statement
			if len(ex) > 100 {
				ex = ex[:97] + "..."
			}
			examples = append(examples, fmt.Sprintf("  %d. \"%s\"", i+1, ex))
		}

		hint := fmt.Sprintf(`[Calibration Check] You expressed confidence about code correctness but haven't run verification since the last edit:

%s

Claims like "this definitely works" or "the fix is complete" without running tests/build are overconfidence -- a known agent calibration gap (EpiCaR, 2025). Before asserting completion, run the relevant build/test/lint command to verify. If you've already started verification in the background, check its output before claiming success.`, strings.Join(examples, "\n"))

		return hint
	}

	// If code wasn't edited at all but agent claims things are fixed,
	// this is even worse -- pure unverified assertion
	if !s.codeEdited && !s.hasRecentVerification() && len(claims) >= 2 {
		s.warnings++
		return `[Calibration Check] Multiple success claims detected without any code edits or verification in this turn. Ensure claims about correctness are backed by actual changes and test runs, not just assertions.`
	}

	return ""
}
