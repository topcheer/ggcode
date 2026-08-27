package agent

// Redundant Re-verification Detector -- Verification Idempotency Violation
//
// Research basis:
//   - SICA (arXiv:2504.15228, NeurIPS 2025): trajectory waste as primary agent
//     efficiency bottleneck -- redundant actions that produce no new information.
//   - Agent-R (arXiv:2506.20469, 2025): self-correction via trajectory trimming
//     identifies repeat actions with identical preconditions as waste.
//   - ACE (ICLR 2026): context-efficient agent frameworks flag re-execution of
//     deterministic operations whose inputs haven't changed.
//
// Problem: Coding agents sometimes re-run verification commands (go build, go
// test, make lint, npm test) without having made any file modifications since
// the previous run. Verification is idempotent: if no source files changed, the
// result will be identical. Re-running it wastes a full iteration + context
// budget on a foregone conclusion.
//
// This is distinct from:
//   - futile_cycle: tracks re-READS (information gathering without mutation)
//   - phantom_verify: claims verification success without running commands
//   - verify_scope_decay: progressive narrowing of verification scope
//   - verify_disconnect: verification failures that get advanced past
//
// This detector specifically catches: "you ran `go test`, it passed, you made
// no edits, then you ran `go test` again." The second run cannot produce new
// information.
//
// Design:
//   - Zero LLM cost -- pure deterministic tool-call + edit tracking.
//   - Tracks verification command categories and the file-edit count since each
//     category was last run.
//   - Non-blocking advisory hint, capped at 2 injections per run.
//   - Resets each user turn.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	redundantReverifyMaxWarnings = 2
)

// redundantReverifyState tracks verification commands and file modifications to
// detect idempotent re-verification (same command, no intervening edits).
type redundantReverifyState struct {
	warnings int
	lastRun  map[string]*reverifyRun // category -> last run info
}

// reverifyRun records the state of the last verification command for a category.
type reverifyRun struct {
	toolName    string
	iteration   int
	editsSince  int    // file modifications since this verification ran
	resultError bool   // did the last run produce an error?
	signature   string // normalized command fingerprint (issue #1173)
}

// isVerificationCommand checks if a tool name + arguments constitutes a
// verification command (build, test, lint, typecheck, etc.).
var reverifyCmdPatterns = map[string]*regexp.Regexp{
	"build":     regexp.MustCompile(`(?i)\b(go\s+build|make\s+build|cargo\s+build|npm\s+run\s+build|yarn\s+build|pnpm\s+build|cmake\b|meson\b)\b`),
	"test":      regexp.MustCompile(`(?i)\b(go\s+test|make\s+test|cargo\s+test|npm\s+test|npm\s+run\s+test|yarn\s+test|pnpm\s+test|pytest\b|jest\b|vitest\b|mocha\b)\b`),
	"lint":      regexp.MustCompile(`(?i)\b(go\s+vet|golangci-lint|eslint|prettier|clang-tidy|flake8|pylint|rubocop|make\s+lint)\b`),
	"typecheck": regexp.MustCompile(`(?i)\b(tsc\s+--noEmit|tsc\s+-p|mypy\b|pyright\b|pyre\b)\b`),
	"fmtcheck":  regexp.MustCompile(`(?i)\b(gofmt\s+-l|goimports\s+-l|rustfmt\s+--check)\b`),
}

// editToolSet identifies tools that modify files (write operations).
// Now an alias of the canonical sourceMutatingTools superset (#154).
var reverifyEditTools = sourceMutatingTools

func newRedundantReverifyState() *redundantReverifyState {
	return &redundantReverifyState{
		lastRun: make(map[string]*reverifyRun),
	}
}

func (s *redundantReverifyState) reset() {
	s.warnings = 0
	s.lastRun = make(map[string]*reverifyRun)
}

// classifyVerificationCommand returns the verification category if the tool
// name + arguments match a known verification pattern, or "" otherwise.
// #343: only shell executions can RUN a verification command. Text-manipulation
// tools (grep/sed/echo) whose ARGUMENTS mention "go test" were previously
// misclassified as verification runs, so the agent's first REAL `go test` was
// warned about as a redundant re-run. Beyond the tool-name gate, the first
// word of the first pipeline segment must not be a text/shell builtin that
// merely references the command.
var reverifyTextToolFirstWords = map[string]bool{
	"grep": true, "rg": true, "sed": true, "awk": true, "echo": true, "cat": true,
	"printf": true, "tail": true, "head": true, "less": true, "sort": true,
	"uniq": true, "wc": true, "tr": true, "cut": true, "tee": true, "xargs": true,
	"man": true, "which": true, "type": true, "find": true, "ls": true,
}

func firstPipelineSegment(args string) string {
	seg := args
	for _, sep := range []string{"|", ";", "&&", "||"} {
		if i := strings.Index(seg, sep); i >= 0 {
			seg = seg[:i]
		}
	}
	return seg
}

// verificationSignature returns a normalized fingerprint of the first pipeline
// segment of a verification command (issue #1173). Two commands in the same
// category are only redundant when this fingerprint matches: `go test
// ./internal/agent/` and `go test ./internal/config/` must never be treated as
// idempotent re-runs of each other.
func verificationSignature(args string) string {
	fields := strings.Fields(firstPipelineSegment(args))
	// Skip the same crude env prefixes that classification skips.
	for len(fields) > 0 && strings.HasPrefix(fields[0], "$(") {
		fields = fields[1:]
	}
	return strings.ToLower(strings.Join(fields, " "))
}

func (s *redundantReverifyState) classifyVerificationCommand(toolName, args string) string {
	// #343: only command-executing tools can perform verification.
	if toolName != "run_command" && toolName != "start_command" {
		return ""
	}
	// Take the first pipeline segment's first word: if the command itself is a
	// text operation, any "go test" mention is data, not execution.
	fields := strings.Fields(firstPipelineSegment(args))
	for len(fields) > 0 && strings.HasPrefix(fields[0], "$(") { // skip env prefixes crudely
		fields = fields[1:]
	}
	if len(fields) > 0 {
		bin := filepath.Base(fields[0])
		if reverifyTextToolFirstWords[bin] {
			return ""
		}
	}
	combined := toolName + " " + args
	for cat, re := range reverifyCmdPatterns {
		if re.MatchString(combined) {
			return cat
		}
	}
	return ""
}

// recordToolCall tracks verification commands and checks for redundant
// re-verification. Returns a hint message if redundant re-verification detected.
func (s *redundantReverifyState) recordToolCall(toolName, args string, iteration int, resultError bool) string {
	cat := s.classifyVerificationCommand(toolName, args)
	if cat == "" {
		return ""
	}
	// Issue #1173: redundancy requires an identical normalized command, not
	// just the same category. Verification of a different scope or target
	// produces new information and must never be flagged.
	sig := verificationSignature(args)

	prev := s.lastRun[cat]

	// Update the current run entry (will overwrite after checking)
	s.lastRun[cat] = &reverifyRun{
		toolName:    toolName,
		iteration:   iteration,
		editsSince:  0,
		resultError: resultError,
		signature:   sig,
	}

	// Check for redundancy: previous run of same category AND same command
	// exists, no edits since, and the previous run was NOT an error (errors
	// may need re-runs after diagnosis even without edits, so we only flag
	// successful re-runs).
	if prev != nil && prev.editsSince == 0 && !prev.resultError && prev.signature == sig {
		if s.warnings >= redundantReverifyMaxWarnings {
			return ""
		}
		s.warnings++
		hint := fmt.Sprintf(`[Redundant Re-verification] You ran a "%s" verification at iteration %d and it passed. No files have been modified since. Running it again cannot produce new information -- verification is idempotent when no source files change (SICA, NeurIPS 2025). Either make the code change first, or if verification is already done, proceed to the next task step.

Previous run: %s (iteration %d, passed)
This run: %s (iteration %d)`,
			cat, prev.iteration, prev.toolName, prev.iteration, toolName, iteration)
		debug.Log("redundant-reverify", "category=%s prev_iter=%d curr_iter=%d edits_since=%d",
			cat, prev.iteration, iteration, prev.editsSince)
		return hint
	}

	return ""
}

// recordEdit increments the edit counter for all tracked verification
// categories. Called whenever a file-modifying tool is executed.
func (s *redundantReverifyState) recordEdit(toolName string) {
	if !reverifyEditTools[toolName] {
		return
	}
	for _, run := range s.lastRun {
		run.editsSince++
	}
}

func (a *Agent) maybeWarnRedundantReverify(_ string) string {
	return "" // detection is inline in recordToolCall because it needs the tool result
}
