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
	"path/filepath"
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
	strategyFixationMaxTotalWarns = 1 // cap total warnings per run
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

// sfCommandArg extracts the "command" string from a run_command args map.
// A non-string or absent command yields "" (psIsVerifyCommand treats it as
// non-verifying, which is correct: you cannot verify via a non-command).
func sfCommandArg(args map[string]interface{}) string {
	if args == nil {
		return ""
	}
	if s, ok := args["command"].(string); ok {
		return s
	}
	return ""
}

// recordVerification tracks the outcome of a verification tool call (build/test/run).
// If the verification failed, the failure is attributed to the most recently edited file.
func (s *strategyFixationState) recordVerification(toolName string, output string, isError bool) {
	if !isError {
		// A successful verification (green build/test) is a WHOLE-TREE
		// validation: it compiles and tests every file touched by the run,
		// not just the most recently edited one. The documented contract is
		// "3+ edits without ANY successful verification in between", so the
		// green result terminates the edit/failure streak for ALL files.
		// Resetting only lastFile left stale counts on other files able to
		// fire "approach not converging" right after a green build — the
		// opposite of the truth at the worst moment (#485, extending #392).
		// True post-green fixation re-accumulates fresh edits+failures, which
		// is exactly the contract's literal semantics.
		s.fileEdits = make(map[string]int)
		s.fileFailures = make(map[string]int)
		s.lastFile = ""
		return
	}
	// Failed verification: attribute to last edited file ONLY when the output
	// actually names that file. Directory-qualified matching: an occurrence
	// of the base name that carries a DIFFERENT directory prefix (e.g.
	// output mentions internal/agent/agent.go while lastFile is
	// internal/tool/agent.go) is a same-base-name collision, not this file
	// (#485; same family as #393). Bare base-name occurrences (no directory
	// prefix) still attribute, preserving the pre-existing sensitivity.
	if s.lastFile != "" {
		if sfOutputNamesFile(output, s.lastFile) {
			s.fileFailures[s.lastFile]++
		}
	}
}

// sfOutputNamesFile reports whether output names the file at filePath.
// Matching rules per occurrence of the base name:
//   - occurrence preceded by a directory segment ("d/base.go"): counts only
//     if that directory's base equals the file's directory base
//   - bare occurrence (no directory prefix): counts (conservative default)
func sfOutputNamesFile(output, filePath string) bool {
	fname := shortFileName(filePath)
	if fname == "" || output == "" {
		return false
	}
	dirBase := shortFileName(strings.TrimSuffix(filepath.ToSlash(filepath.Dir(filePath)), "/"))
	if dirBase == "." || dirBase == "/" {
		dirBase = ""
	}
	for idx := 0; ; {
		i := strings.Index(output[idx:], fname)
		if i < 0 {
			return false
		}
		i += idx
		// Determine the path prefix immediately before this occurrence.
		// Stop only at NON-PATH delimiters (whitespace, parens, quotes):
		// '/' and '\\' belong to the directory prefix — stopping at them
		// would misread "internal/agent/agent.go" as a bare occurrence.
		start := i
		for start > 0 {
			c := output[start-1]
			if c == ' ' || c == '\t' || c == '\n' || c == '(' || c == '"' || c == '\'' || c == '`' {
				break
			}
			start--
		}
		seg := output[start:i] // e.g. "internal/agent/" or "" or "foo"
		if seg == "" || seg == "." || seg == "./" {
			// Bare occurrence (line start, whitespace, quote): attribute.
			return true
		}
		// The occurrence has a path prefix; its last directory component
		// must match the file's directory base.
		trimmed := strings.TrimRight(seg, "/\\")
		dir := shortFileName(trimmed)
		if dir == "." || dir == "" {
			return true
		}
		if dirBase != "" && strings.EqualFold(dir, dirBase) {
			return true
		}
		// Different directory — try next occurrence.
		idx = i + len(fname)
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
// Derived from the canonical sourceMutatingTools superset (#737, fixing the
// #153/#154 drift where only 5 of the 9 mutation tools were tracked —
// batch_replace/lsp_rename/file_ops/multi_file_write codemod retries were
// silently untracked, the most typical fixation scenario).
func strategyFixationIsMutation(toolName string) bool {
	return sourceMutatingTools[toolName]
}

// strategyFixationIsVerification returns true for tools that verify correctness.
// Note: run_command/start_command are verification-shaped but NOT every
// invocation verifies — `cat`, `ls`, `git status` succeeding is not a green
// build. The wiring in agent.go filters those through psIsVerifyCommand
// (the same command-position analysis used by premature_success, #483) so
// non-verifying commands neither reset streaks nor inject failures (#485).
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
