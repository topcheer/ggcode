package agent

// Temporal Blindness Detector - Verification Staleness Across Mutations
//
// Research basis: "Your LLM Agents are Temporally Blind: The Misalignment
// Between Tool Use Decisions and Human Time Perception" (arXiv:2510.23853,
// 2025) identifies that LLM agents fail to account for real-world state
// changes between observations and subsequent actions. The paper shows agents
// exhibit "over-reliance" (acting on stale context) and "under-reliance"
// (redundantly re-checking stable facts). Even when given timestamp info, the
// best model achieves only 65% alignment with human temporal perception.
//
// In coding agents, the most impactful temporal blindness failure is:
//   1. Agent runs build/test/lint → succeeds
//   2. Agent makes N edits (mutating the codebase)
//   3. Agent references the pre-mutation verification as still valid
//      ("the build passes", "all tests green", "as verified earlier")
//
// This is temporally blind because the mutations may have invalidated the
// verification result. The agent is treating a stale observation as current.
//
// Distinction from existing detectors:
//   - expired_read_check.go: same-file read→edit invalidation (content-level)
//   - verify_disconnect.go: verification FAILED but agent advanced anyway
//   - verify_debt.go: modification-vs-verification ratio (aggregate tracking)
//   - unverified_confidence.go: no verification was ever run after edits
//
// THIS detector fills the orthogonal gap: warns when the agent TEXTUALLY
// REFERENCES a verification result as still valid AFTER mutations have
// occurred since that verification. The verification is not known to be wrong
// (it succeeded) — but it's temporally stale because subsequent mutations may
// have broken things. The agent should re-verify rather than trusting the
// pre-mutation result.
//
// Design:
//   - Zero LLM cost - pure state tracking + regex on assistant text
//   - Fires at most 2 times per run to avoid flooding
//   - Non-blocking: hint injected as user message, agent continues normally
//   - Threshold: 3+ mutations since last verified state → staleness risk

import (
	"fmt"
	"regexp"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// mutationsBeforeStale: minimum number of mutations (edits/writes/file_ops)
	// after a successful verification before we consider it potentially stale.
	// Below this threshold, the agent is likely still in the same edit session.
	mutationsBeforeStale = 3

	// maxTemporalBlindnessWarnings: cap total notices per run.
	maxTemporalBlindnessWarnings = 2
)

// temporalBlindnessState tracks verification staleness across mutations.
type temporalBlindnessState struct {
	// lastVerifiedSeq is the sequence number of the most recent successful
	// verification (build/test/lint that passed). Zero means none yet.
	lastVerifiedSeq int

	// lastVerifiedTool records which verification tool was used.
	lastVerifiedTool string

	// mutationCount tracks mutations since lastVerifiedSeq.
	mutationCount int

	// warningCount is the total notices issued this run.
	warningCount int

	// seq is a monotonically increasing counter for event ordering.
	seq int

	// firedAfterMutations tracks the mutation count when the last warning
	// fired, so we only re-warn after significant additional mutations.
	firedAfterMutations int
}

func newTemporalBlindnessState() *temporalBlindnessState {
	return &temporalBlindnessState{}
}

// verificationToolNames maps tool names that produce verification signals.
var temporalBlindnessVerifyTools = map[string]bool{
	"run_command":   true, // build/test/lint often via run_command
	"ci_status":     true,
	"start_command": true, // background builds
}

// mutationToolNames maps tools that mutate the codebase.
var temporalBlindnessMutationTools = map[string]bool{
	"edit_file":       true,
	"write_file":      true,
	"multi_edit_file": true,
	"multi_file_edit": true,
	"file_ops":        true,
}

// verificationSuccessPattern detects successful verification output.
var verificationSuccessPattern = regexp.MustCompile(
	`(?i)(?:build (?:successful|complete|ok|pass)|PASS|ok\b|0 error|no error|compil(?:ed|ation) (?:successful|complete)|tests? pass|all tests pass|\d+ passed)`,
)

// stalenessClaimPattern detects assistant text claiming verification is valid.
var stalenessClaimPattern = regexp.MustCompile(
	`(?i)\b(?:the (?:build|test|compilation|code) (?:pass(?:es|ed)?|is (?:green|ok|clean))|all tests? (?:pass|passed|green|ok)|verif(?:ied|ication (?:pass|confirms?))|confirm(?:ed|s) (?:it |that |this )?(?:(?:build|test|compil)|work)|as (?:shown|confirmed|verified) (?:by|earlier|above)|build (?:succeed|pass)|compilation (?:pass(?:es|ed)?|succeed(?:ed)?)|lint(?:er)? (?:is )?(?:clean|pass(?:es|ed)?))\b`,
)

// recordVerification tracks a verification tool call and its result.
// If the verification succeeded, we record the sequence number.
func (t *temporalBlindnessState) recordVerification(toolName, _, result string, seq int) {
	if !temporalBlindnessVerifyTools[toolName] {
		return
	}
	// Check if the result indicates success.
	if verificationSuccessPattern.MatchString(result) {
		t.lastVerifiedSeq = seq
		t.lastVerifiedTool = toolName
		t.mutationCount = 0
		t.firedAfterMutations = 0
	}
}

// recordMutation increments the mutation counter when code-mutating tools are used.
func (t *temporalBlindnessState) recordMutation(toolName string) {
	if temporalBlindnessMutationTools[toolName] {
		t.mutationCount++
		t.seq++
	}
}

// maybeWarnTemporalBlindness scans assistant text for verification claims
// that reference stale (pre-mutation) results. Returns a guidance hint if
// detected, empty string otherwise.
func (t *temporalBlindnessState) maybeWarnTemporalBlindness(assistantText string) string {
	if t.warningCount >= maxTemporalBlindnessWarnings {
		return ""
	}
	if t.lastVerifiedSeq == 0 {
		return "" // no prior verification to be stale
	}
	if t.mutationCount < mutationsBeforeStale {
		return "" // not enough mutations to be concerned
	}
	// Only re-warn after significant additional mutations since last warning.
	if t.warningCount > 0 && t.mutationCount-t.firedAfterMutations < mutationsBeforeStale {
		return ""
	}

	// Check if the agent is claiming verification is still valid.
	if !stalenessClaimPattern.MatchString(assistantText) {
		return ""
	}

	t.warningCount++
	t.firedAfterMutations = t.mutationCount

	debug.Log("agent", "temporal blindness: %d mutations since last verification (%s), agent claims results still valid",
		t.mutationCount, t.lastVerifiedTool)

	return fmt.Sprintf(
		"[temporal-blindness] You referenced a verification result (%s) as still valid, "+
			"but %d code mutations have occurred since that check. "+
			"The verification is temporally stale - subsequent edits may have introduced errors. "+
			"Re-run the build/test/lint before claiming success. Do not rely on pre-mutation verification results as current.",
		t.lastVerifiedTool, t.mutationCount,
	)
	// Note: format string above is constant, lastVerifiedTool and mutationCount are safe values.
}

// reset clears state for a new run.
func (t *temporalBlindnessState) reset() {
	t.lastVerifiedSeq = 0
	t.lastVerifiedTool = ""
	t.mutationCount = 0
	t.warningCount = 0
	t.seq = 0
	t.firedAfterMutations = 0
}
