package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Unverified Success Claim Detector
//
// Research basis: "Premature Completion" anti-pattern (agentpatterns.ai, 2026),
// SRI Lab/ETH Zurich "Fixing correct code" study, and SWE-EVO (arxiv 2512.18470)
// "Premature Termination" failure mode. All four independent teams found that
// agents produce confident-sounding success claims ("tests pass", "build succeeds",
// "all fixed") in their text WITHOUT having actually run verification commands.
//
// The gap: ggcode has sync-verify (build check after edits), fulfillment_gate
// (keyword overlap at exit), and verification_debt (accumulated edit tracking).
// But NONE of them cross-reference the agent's NATURAL LANGUAGE claims against
// actual verification tool usage. An agent that says "all tests pass" in its
// final response but never ran `go test` or any test command is making an
// unverified success claim.
//
// This detector fills that gap with a deterministic, zero-LLM-cost heuristic:
//   1. CLAIM DETECTION: scan the agent's response text for success phrases
//      ("tests pass", "build succeeds", "verified", "all passing", etc.)
//   2. VERIFICATION CHECK: cross-reference against CommandsRun to see if a
//      build/test/verification command was actually executed
//   3. CROSS-CHECK: also check if verify-related tools (lsp_diagnostics, etc.)
//      were used as alternative verification
//
// The detector fires AT MOST ONCE per run, injecting a reminder that gives
// the agent a chance to actually run verification before returning.

const maxUnverifiedClaimWarnings = 1

// successClaimPhrases are phrases that indicate the agent is asserting
// verification results in its response text. These are lowercased for matching.
var successClaimPhrases = []string{
	"tests pass", "all tests pass", "test passes", "tests are passing",
	"all passing", "tests passing", "unit tests pass",
	"build passes", "build succeeds", "build is successful",
	"build passes successfully", "compiles successfully", "compiles fine",
	"lint clean", "linting passes", "lint passes",
	"verified", "verification passed", "verification successful",
	"confirmed working", "confirmed it works", "confirmed the fix",
	"validated", "validation passed",
	"all green", "everything passes", "all checks pass",
	"ci passes", "ci will pass",
	"no errors", "no compilation errors", "no test failures",
	"all tests successful", "tests are successful",
	"type checks", "type checking passes",
}

// verificationToolNames are tools that serve as alternative verification
// (beyond explicit build/test shell commands).
var verificationToolNames = map[string]bool{
	"lsp_diagnostics": true,
	"code_health":     true,
	"review_changes":  true,
	"scan_todos":      true,
}

// unverifiedClaimState tracks whether the detector has already fired this run.
type unverifiedClaimState struct {
	fired bool
}

func newUnverifiedClaimState() *unverifiedClaimState {
	return &unverifiedClaimState{}
}

func (u *unverifiedClaimState) reset() {
	u.fired = false
}

// checkUnverifiedClaim scans the agent's response for success claims that
// lack corresponding verification tool usage. Returns a non-empty message
// if an unverified claim is detected.
//
// Parameters:
//   - assistantText: the agent's final response text
//   - runStats: accumulated stats from the run
//
// Returns "" when:
//   - the detector already fired this run
//   - no success claims found in the response
//   - verification commands were actually run
//   - verification tools (diagnostics, etc.) were used
func (a *Agent) checkUnverifiedClaim(assistantText string, runStats *RunStats) string {
	if a.unverifiedClaim.fired {
		return ""
	}

	text := strings.ToLower(assistantText)
	if len(strings.TrimSpace(text)) < 5 {
		return ""
	}

	// Step 1: Detect success claims in the response text.
	claims := detectSuccessClaims(text)
	if len(claims) == 0 {
		return ""
	}

	// Step 2: Check if build/test verification commands were actually run.
	if hasVerificationCommands(runStats) {
		debug.Log("unverified-claim", "claims found (%v) but verification commands present — OK", claims[:min(3, len(claims))])
		return ""
	}

	// Step 3: Check if verification-related tools were used as alternative.
	if hasVerificationTools(runStats) {
		debug.Log("unverified-claim", "claims found (%v) but verification tools present — OK", claims[:min(3, len(claims))])
		return ""
	}

	// Claims found but no verification performed — inject reminder.
	a.unverifiedClaim.fired = true
	debug.Log("unverified-claim", "unverified success claims detected: %v", claims[:min(3, len(claims))])

	return fmt.Sprintf(
		"Before finishing: your response claims success (%s), but no build, test, or verification commands "+
			"were run during this session. If you are asserting that code compiles, tests pass, or lint is clean, "+
			"run the appropriate verification command (e.g. build, test, lint) to confirm. "+
			"If your claims refer to pre-existing conditions or analysis rather than new verification, "+
			"clarify that explicitly in your response.",
		strings.Join(claims[:min(3, len(claims))], ", "),
	)
}

// detectSuccessClaims returns the matching success-claim phrases found in text.
func detectSuccessClaims(text string) []string {
	var found []string
	seen := make(map[string]bool)
	for _, phrase := range successClaimPhrases {
		if strings.Contains(text, phrase) && !seen[phrase] {
			found = append(found, phrase)
			seen[phrase] = true
		}
	}
	return found
}

// hasVerificationCommands checks whether any build/test/lint command was run.
func hasVerificationCommands(runStats *RunStats) bool {
	for _, cmd := range runStats.CommandsRun {
		lower := strings.ToLower(stripCommandComment(cmd))
		if isBuildTestCommand(lower) {
			return true
		}
	}
	return false
}

// hasVerificationTools checks whether verification-related tools were used.
func hasVerificationTools(runStats *RunStats) bool {
	for toolName := range runStats.ToolCalls {
		if verificationToolNames[toolName] {
			return true
		}
	}
	return false
}
