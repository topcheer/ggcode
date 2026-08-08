package agent

// Unverified Self-Diagnosis Detector -- Correlated Failure Detection
//
// Research basis:
//   - MetaCognition Patterns for AI Agent Self-Monitoring (zylos.ai, 2026):
//     Section 2.2 "Correlated Failure": "A model that systematically
//     misunderstands a task will generate wrong outputs, critique them with
//     wrong standards, and refine them toward wrong conclusions -- without
//     ever triggering an anomaly flag." This is the core insight: when the
//     SAME model that caused an error diagnoses why the error occurred, the
//     diagnosis shares the same biases. The evaluator must be structurally
//     independent of the actor.
//   - Self-Refine (Madaan et al., 2023): identifies the correlated failure
//     problem explicitly -- "the model's critic and the model's generator share
//     all the same biases."
//   - Reflexion (Shinn et al., 2023): solves this by separating actor and
//     evaluator. Without separation, self-diagnosis is unreliable.
//   - AgentDebug (arXiv:2509.25370): identifies "performative diagnosis" where
//     agents produce plausible-sounding root cause explanations that are
//     incorrect, leading to ineffective fixes.
//
// THE CORRELATED FAILURE PROBLEM IN CODING AGENTS:
// When an agent encounters an error (build failure, test failure, edit failure),
// it frequently responds with an immediate self-diagnosis:
//
//   "The error is caused by the missing import..."
//   "This fails because the function signature changed..."
//   "The issue is that the struct doesn't have that field..."
//
// And then immediately implements a fix based on that diagnosis -- WITHOUT
// independently verifying the diagnosis (re-reading the actual error output,
// checking the actual file state, or running a diagnostic command).
//
// This is "correlated failure": the same model that wrote the code that failed
// is now diagnosing why it failed. Its diagnosis may be wrong for exactly the
// same reasons the code was wrong. The fix then targets the wrong cause,
// leading to compounding errors.
//
// THE VERIFICATION GAP:
// The correct pattern is: encounter error → READ THE ACTUAL ERROR → verify
// diagnosis against ground truth → THEN fix. But agents often skip the
// verification step, jumping straight from error to self-diagnosis to fix.
//
// This detector identifies when the agent:
//   1. Recently encountered a tool error (build, test, edit, run failure)
//   2. Makes a definitive diagnosis claim in its response
//   3. Does NOT include evidence of verification (no read_file, grep, lsp_*
//      diagnostic call between the error and the diagnosis)
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - diagnostic_disconnect.go: checks if diagnostics from iter N are addressed
//     in iter N+1. This is about diagnosis FOLLOW-THROUGH, not diagnosis QUALITY.
//   - evidence_overconfidence.go: certainty derived from evidence tools. This is
//     about certainty WITHOUT evidence tools -- the agent diagnoses from memory.
//   - assumption_track.go: general hedging language. This is specifically about
//     diagnostic certainty after errors without verification.
//   - verification_debt.go: tracks unverified code changes, not unverified
//     diagnoses.
//   - tool_target_mismatch.go: checks if tool targets match intent, not if
//     diagnosis was verified.
//
// Design:
//   - Tracks tool errors encountered in the recent window
//   - Scans assistant text for definitive diagnostic claims after errors
//   - Checks whether verification tools (read_file, grep, lsp_*) were called
//     between the error and the diagnosis
//   - Zero LLM cost - pure deterministic pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// selfDiagMaxWarnings: max warnings per run.
	selfDiagMaxWarnings = 2

	// selfDiagErrorWindow: how many recent iterations to look back for errors.
	selfDiagErrorWindow = 3

	// selfDiagMaxExamples: max diagnosis examples to include.
	selfDiagMaxExamples = 3
)

// selfDiagDiagnosisPattern matches definitive diagnostic claims about errors.
// These are phrases that assert a root cause with certainty.
var selfDiagDiagnosisPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bthe (?:error|issue|problem|failure|cause) is\b`),
	regexp.MustCompile(`(?i)\bthis (?:fails|errors|breaks) because\b`),
	regexp.MustCompile(`(?i)\bthis is caused by\b`),
	regexp.MustCompile(`(?i)\bthe root cause is\b`),
	regexp.MustCompile(`(?i)\bwhat's happening is\b`),
	regexp.MustCompile(`(?i)\bwhat is happening is\b`),
	regexp.MustCompile(`(?i)\bthe problem (?:is|was) (?:that|the|a|we)\b`),
	regexp.MustCompile(`(?i)\bit'?s (?:failing|erroring) (?:because|due to|since)\b`),
	regexp.MustCompile(`(?i)\bdue to (?:the |a |an )?(?:missing|incorrect|wrong|invalid)\b`),
	regexp.MustCompile(`(?i)\bI (?:see|know) (?:why|the (?:issue|problem))\b`),
	regexp.MustCompile(`(?i)\bthat'?s because\b`),
	regexp.MustCompile(`(?i)\bthe fix is (?:to|simple|straightforward)\b`),
}

// selfDiagVerificationTool identifies tools that would verify a diagnosis.
// If any of these were called between the error and the diagnosis claim,
// the diagnosis is considered "verified" and we don't warn.
var selfDiagVerificationTools = map[string]bool{
	"read_file":             true,
	"multi_file_read":       true,
	"grep":                  true,
	"search_files":          true,
	"lsp_diagnostics":       true,
	"lsp_hover":             true,
	"lsp_definition":        true,
	"lsp_references":        true,
	"lsp_workspace_symbols": true,
	"code_search":           true,
}

// selfDiagErrorTool identifies tools whose failure constitutes an "error event."
var selfDiagErrorTools = map[string]bool{
	"run_command":     true,
	"edit_file":       true,
	"multi_edit_file": true,
	"write_file":      true,
	"multi_file_edit": true,
}

// selfDiagEntry records a tool call or error in the trajectory.
type selfDiagEntry struct {
	iteration      int
	toolName       string
	isError        bool
	isVerification bool
}

// selfDiagState tracks the trajectory for correlated failure detection.
type selfDiagState struct {
	warnings  int
	entries   []selfDiagEntry // recent tool entries
	diagCount int             // how many unverified diagnoses detected
}

func newSelfDiagState() *selfDiagState {
	return &selfDiagState{}
}

func (s *selfDiagState) reset() {
	s.warnings = 0
	s.entries = nil
	s.diagCount = 0
}

// recordToolCall records a tool call in the trajectory.
func (s *selfDiagState) recordToolCall(iteration int, toolName string, isError bool) {
	entry := selfDiagEntry{
		iteration:      iteration,
		toolName:       toolName,
		isError:        isError,
		isVerification: selfDiagVerificationTools[toolName],
	}
	s.entries = append(s.entries, entry)

	// Keep only recent entries (sliding window).
	if len(s.entries) > 30 {
		s.entries = s.entries[len(s.entries)-30:]
	}
}

// hasRecentError checks if there was a tool error in the last N iterations.
func (s *selfDiagState) hasRecentError(currentIter int) (bool, int) {
	for i := len(s.entries) - 1; i >= 0; i-- {
		e := s.entries[i]
		if currentIter-e.iteration > selfDiagErrorWindow {
			break
		}
		if e.isError && selfDiagErrorTools[e.toolName] {
			return true, e.iteration
		}
	}
	return false, 0
}

// hadVerificationSinceError checks whether any verification tool was called
// between the error iteration and the current iteration.
func (s *selfDiagState) hadVerificationSinceError(errorIter, currentIter int) bool {
	for _, e := range s.entries {
		if e.iteration > errorIter && e.iteration <= currentIter {
			if e.isVerification {
				return true
			}
		}
	}
	return false
}

// scanDiagnosisClaims finds definitive diagnostic claims in the text.
func scanDiagnosisClaims(text string) []string {
	if len(text) == 0 {
		return nil
	}

	var hits []string
	seen := make(map[string]bool)

	for _, pat := range selfDiagDiagnosisPatterns {
		locs := pat.FindAllStringIndex(text, -1)
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
			if len(excerpt) > 80 {
				excerpt = excerpt[:80] + "..."
			}
			if seen[excerpt] {
				continue
			}
			seen[excerpt] = true
			hits = append(hits, excerpt)
		}
	}

	return hits
}

// maybeWarnSelfDiagnosis checks for unverified self-diagnosis after errors.
// Returns a guidance message if detected, empty string otherwise.
func (a *Agent) maybeWarnSelfDiagnosis(text string, currentIter int) string {
	if a.selfDiagState == nil {
		return ""
	}
	if a.selfDiagState.warnings >= selfDiagMaxWarnings {
		return ""
	}

	// Check if there was a recent error.
	hasError, errorIter := a.selfDiagState.hasRecentError(currentIter)
	if !hasError {
		return ""
	}

	// Check for diagnosis claims in the text.
	claims := scanDiagnosisClaims(text)
	if len(claims) == 0 {
		return ""
	}

	// Check whether the agent verified the diagnosis.
	verified := a.selfDiagState.hadVerificationSinceError(errorIter, currentIter)
	if verified {
		return ""
	}

	// Unverified self-diagnosis detected.
	a.selfDiagState.warnings++
	a.selfDiagState.diagCount++

	var examples []string
	for i, c := range claims {
		if i >= selfDiagMaxExamples {
			break
		}
		examples = append(examples, fmt.Sprintf("  %d. ...%s...", i+1, c))
	}

	severity := "WARNING"
	if a.selfDiagState.diagCount >= 2 {
		severity = "CRITICAL"
	}

	return fmt.Sprintf(
		"[%s-self-diagnosis] Detected %d definitive diagnosis claim(s) about a recent "+
			"tool error, but no verification tool (read_file, grep, lsp_diagnostics) was "+
			"called to confirm the diagnosis. This is a CORRELATED FAILURE risk: the same "+
			"model that generated the failing code is now diagnosing why it failed -- the "+
			"diagnosis may be wrong for the same reasons the code was wrong "+
			"(Self-Refine correlated failure, Madaan et al. 2023). "+
			"Before implementing a fix, VERIFY the diagnosis: re-read the actual error "+
			"output, check the actual file state, or run a diagnostic command.\n"+
			"Unverified diagnoses:\n%s",
		severity, len(claims),
		strings.Join(examples, "\n"),
	)
}
