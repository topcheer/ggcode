package agent

// strategy_fixation.go -- Strategy Fixation Detector
//
// Research basis: PARC (arXiv:2512.03549) identifies that standard coding agents
// suffer from "approach-level failures" -- repeatedly applying the same strategy
// class to a problem that isn't converging, without recognizing the approach
// itself is wrong. Unlike local tool-level failures (wrong arguments, syntax
// errors), approach-level failures are systemic: the agent keeps hammering the
// same file or symbol region across many iterations, making incremental tweaks
// that never achieve convergence (successful build/test pass).
//
// This detector identifies when the agent has edited the same file 3+ times
// without any successful verification in between, indicating it is stuck in a
// strategy fixation loop. The guidance urges the agent to step back and consider
// an alternative approach rather than continuing to apply the same strategy.
//
// Distinct from existing detectors:
//   - bareEditStreak: counts consecutive mutations without ANY verification (tool-agnostic)
//   - editOscillation: detects semantic back-and-forth (adding then removing same code)
//   - correctionSpiral: tracks error severity escalation across fixes
//   - verifyDebt: accumulates edits since last green build (no file-scoping)
//   - This detector: file-scoped strategy fixation -- same file edited N times
//     with intervening FAILED verifications (not absent verification, but active
//     failure), proving the approach to that file isn't working.

import (
	"fmt"
	"strings"
)

// strategyFixationState tracks per-file edit counts and verification outcomes.
type strategyFixationState struct {
	// fileEdits counts edits per file path in this run
	fileEdits map[string]int
	// fileFailures counts failed verifications (build/test errors) per file
	fileFailures map[string]int
	// lastFile tracks the most recently edited file (for linking verification failures)
	lastFile string
	// warnedFiles prevents re-warning the same file
	warnedFiles map[string]bool
	// warnCount caps total warnings per run
	warnCount int
}

const (
	strategyFixationEditThreshold = 3 // 3+ edits to the same file triggers analysis
	strategyFixationFailThreshold = 2 // with 2+ associated failed verifications
	strategyFixationMaxTotalWarns = 2 // cap total warnings per run
)

func newStrategyFixationState() *strategyFixationState {
	return &strategyFixationState{
		fileEdits:    make(map[string]int),
		fileFailures: make(map[string]int),
		warnedFiles:  make(map[string]bool),
	}
}

func (s *strategyFixationState) reset() {
	s.fileEdits = make(map[string]int)
	s.fileFailures = make(map[string]int)
	s.lastFile = ""
	s.warnedFiles = make(map[string]bool)
	s.warnCount = 0
}

// recordEdit tracks a file mutation (edit_file, write_file, multi_edit_file).
func (s *strategyFixationState) recordEdit(filePath string) {
	if filePath == "" {
		return
	}
	s.fileEdits[filePath]++
	s.lastFile = filePath
}

// recordVerification tracks the outcome of a verification tool call (build/test/run).
// If the verification failed, the failure is attributed to the most recently edited file.
func (s *strategyFixationState) recordVerification(toolName string, output string, isError bool) {
	if !isError {
		// Successful verification resets failure count for the last edited file
		// -- the approach is converging.
		if s.lastFile != "" {
			s.fileFailures[s.lastFile] = 0
		}
		return
	}
	// Failed verification: attribute to last edited file
	if s.lastFile != "" {
		// Only count if the output seems related to the file (heuristic: file name
		// appears in error output, or it's a generic build/test failure)
		fname := shortFileName(s.lastFile)
		if strings.Contains(output, fname) || strings.Contains(output, "build") ||
			strings.Contains(output, "compile") || strings.Contains(output, "FAIL") ||
			strings.Contains(output, "error") || strings.Contains(output, "undefined") {
			s.fileFailures[s.lastFile]++
		}
	}
}

// check returns guidance if any file shows strategy fixation pattern.
func (s *strategyFixationState) check() string {
	if s.warnCount >= strategyFixationMaxTotalWarns {
		return ""
	}

	for file, editCount := range s.fileEdits {
		if editCount < strategyFixationEditThreshold {
			continue
		}
		if s.warnedFiles[file] {
			continue
		}
		failCount := s.fileFailures[file]
		if failCount < strategyFixationFailThreshold {
			continue
		}

		// Strategy fixation detected
		s.warnedFiles[file] = true
		s.warnCount++
		fname := shortFileName(file)
		return fmt.Sprintf(
			"[strategy-fixation] Edited %s %d times with %d failures. Approach not converging - re-read file or try different strategy.",
			fname, editCount, failCount,
		)
	}
	return ""
}

// shortFileName extracts the base name from a file path.
func shortFileName(path string) string {
	if path == "" {
		return ""
	}
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

// strategyFixationIsMutation returns true for tools that modify files.
func strategyFixationIsMutation(toolName string) bool {
	switch toolName {
	case "edit_file", "write_file", "multi_edit_file", "multi_file_edit", "notebook_edit":
		return true
	default:
		return false
	}
}

// strategyFixationIsVerification returns true for tools that verify correctness.
func strategyFixationIsVerification(toolName string) bool {
	switch {
	case toolName == "run_command",
		toolName == "start_command",
		toolName == "code_health",
		toolName == "review_changes",
		toolName == "verify",
		toolName == "lsp_diagnostics":
		return true
	default:
		return false
	}
}
