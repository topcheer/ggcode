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
	"encoding/json"
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
func (s *bareEditStreakState) recordToolCall(toolName string, toolInput string) {
	switch {
	case bareStreakIsMutation(toolName):
		s.streak++
	case bareStreakIsVerification(toolName, toolInput):
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
// Derived from the canonical sourceMutatingTools superset (#738).
func bareStreakIsMutation(toolName string) bool {
	return sourceMutatingTools[toolName]
}

// bareStreakIsVerification returns true for tools that verify correctness.
// toolInput is the raw JSON arguments (fix #141: check command content, not just name).
func bareStreakIsVerification(toolName string, toolInput string) bool {
	switch {
	case toolName == "run_command", toolName == "start_command":
		// Only reset streak if the command actually verifies code
		// (fix #141 bug 1: "echo done" or "ls" should not reset streak).
		return commandIsVerification(toolInput)
	case toolName == "lsp_diagnostics",
		toolName == "lsp_references",
		toolName == "lsp_definition",
		toolName == "code_health",
		toolName == "review_changes",
		toolName == "verify":
		return true
	// fix #141 bug 2: git_add, git_commit, git_checkout, git_reset, git_revert,
	// git_stash are mutations, NOT verification. Only git_diff and git_status
	// are truly read-only verification tools.
	case toolName == "git_diff", toolName == "git_status":
		return true
	default:
		return false
	}
}

// commandIsVerification checks if a run_command/start_command input actually
// verifies code correctness (fix #141). Commands like "echo", "ls", "pwd",
// "cat", "git add", "git commit" are NOT verification.
//
// #748: delegate to the position-aware psIsVerifyCommand (premature_success.go)
// instead of a local prefix list - the prefix-only match missed "cd dir && go
// test", env-prefixed (GOFLAGS=... make verify-ci), and "# comment"-prefixed
// commands, producing false "no verification" warnings right after a green
// test run. psIsVerifyCommand handles &&-segments, env prefixes, and
// build-system target whitelists (#350/#483/#553) and is already reused by
// four other detectors.
func commandIsVerification(toolInput string) bool {
	if toolInput == "" {
		return false // no command info → don't assume verification
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(toolInput), &args); err != nil {
		return false
	}
	return psIsVerifyCommand(stripCommandPrefixes(args.Command))
}

// stripCommandPrefixes removes leading '#' comment lines and leading VAR=value
// env assignments so position-aware matching sees the true command position.
// psIsVerifyCommand's build-system dispatch keys on tokens[0], which for
// env-prefixed invocations (GOFLAGS=... make verify-ci) is the assignment
// itself, so the whitelist never sees "make" (#748).
func stripCommandPrefixes(cmd string) string {
	var real []string
	for _, line := range strings.Split(cmd, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Fields(t)
		for len(fields) > 0 && isEnvAssign(fields[0]) {
			fields = fields[1:]
		}
		real = append(real, fields...)
	}
	return strings.Join(real, " ")
}

// isEnvAssign reports whether tok looks like a leading VAR=value assignment.
func isEnvAssign(tok string) bool {
	i := strings.Index(tok, "=")
	if i <= 0 {
		return false
	}
	for _, r := range tok[:i] {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
