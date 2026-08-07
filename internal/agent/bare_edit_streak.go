package agent

// bare_edit_streak.go — Unverified Mutation Streak Detector
//
// Research basis: A well-established principle in 2025 AI agent engineering
// (Anthropic "effective agents", OpenAI agentic best practices, SWE-bench
// trajectory analyses) is that TIGHT FEEDBACK LOOPS are critical for coding
// agents. When an agent makes many consecutive file mutations (edits/writes)
// without any verification step (build/test/run/diagnostic) in between, it is
// operating "blind" -- errors accumulate and must be debugged in a larger,
// more expensive batch later. This detector identifies that streak pattern
// and nudges the agent to verify before continuing.
//
// This is distinct from existing detectors: it focuses on the STREAK of
// consecutive mutations with zero intervening verification, not on tool
// diversity, oscillation, debt, or claim verification.

import (
	"fmt"
	"strings"
)

// bareEditStreakState tracks consecutive mutations without verification.
type bareEditStreakState struct {
	streak       int // consecutive mutation count without any verification
	warnCount    int // how many warnings emitted this run
	lastWarnedAt int // streak length when last warned (avoid spamming)
}

const (
	bareEditStreakThreshold = 5 // warn after 5 consecutive unverified mutations
	bareEditStreakMaxWarns  = 3 // cap warnings per run
	bareEditStreakRewarnGap = 3 // re-warn only after streak grows by 3 more
)

func newBareEditState() *bareEditStreakState {
	return &bareEditStreakState{}
}

func (s *bareEditStreakState) reset() {
	s.streak = 0
	s.warnCount = 0
	s.lastWarnedAt = 0
}

// recordToolCall classifies a tool call as mutation, verification, or other.
// Mutations increment the streak; verifications reset it; other tools are neutral.
func (s *bareEditStreakState) recordToolCall(toolName string) {
	switch {
	case bareStreakIsMutation(toolName):
		s.streak++
	case bareStreakIsVerification(toolName):
		s.streak = 0
	}
}

// maybeWarn returns guidance if the streak has crossed a warning threshold.
func (s *bareEditStreakState) maybeWarn(_ int) string {
	if s.warnCount >= bareEditStreakMaxWarns {
		return ""
	}
	if s.streak < bareEditStreakThreshold {
		return ""
	}
	// Only re-warn if streak grew significantly since last warning
	if s.warnCount > 0 && s.streak < s.lastWarnedAt+bareEditStreakRewarnGap {
		return ""
	}
	s.warnCount++
	s.lastWarnedAt = s.streak
	return fmt.Sprintf(
		"[feedback-loop] You have made %d consecutive file edits without any "+
			"verification step (build/test/run). Errors may have accumulated. "+
			"Run `go build -tags goolm ./...` or `go test -tags goolm ./...` "+
			"NOW to catch issues before they compound into a larger debugging session.",
		s.streak,
	)
}

// bareStreakIsMutation returns true for tools that modify files.
func bareStreakIsMutation(toolName string) bool {
	switch toolName {
	case "edit_file", "multi_edit_file", "write_file", "multi_file_write",
		"multi_file_edit", "file_ops", "notebook_edit":
		return true
	default:
		return false
	}
}

// bareStreakIsVerification returns true for tools that verify correctness.
func bareStreakIsVerification(toolName string) bool {
	switch {
	case toolName == "run_command",
		toolName == "start_command",
		toolName == "lsp_diagnostics",
		toolName == "lsp_references",
		toolName == "lsp_definition",
		toolName == "code_health",
		toolName == "review_changes",
		toolName == "verify",
		strings.HasPrefix(toolName, "git_"):
		return true
	default:
		return false
	}
}
