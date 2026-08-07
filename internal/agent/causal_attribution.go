package agent

// Causal Failure Attribution Detector — CausalFlow-inspired
//
// Research: Bonagiri et al., "CausalFlow: Causal Attribution and Counterfactual
// Repair for LLM Agent Failures" (arXiv:2605.25338, May 2026).
//
// Key insight: LLM agents frequently fail on multi-step tasks. While such
// failures are typically logged or retried heuristically, they contain
// structured signals about WHERE execution broke down. CausalFlow models
// execution traces as sequential chains of dependent steps and computes
// Causal Responsibility Scores (CRS) via step-level counterfactual
// intervention to identify failure-inducing steps.
//
// THE GAP IN GGCODE:
// When a build/test failure occurs, the agent has many detectors for the
// failure ITSELF (error_classifier, recurring_error, fix_cascade, etc.) but
// NO mechanism to trace backward through the execution trace and identify
// WHICH specific edit step most likely CAUSED the failure. The agent
// blindly tries fixes without knowing the root cause step.
//
// WHAT THIS DETECTOR DOES:
// 1. Maintains a chronological log of edit steps with their target files.
// 2. When a verification step (build/test/go test) fails, it traces
//    backward to attribute the failure to the most likely causal edit.
// 3. Computes a Causal Responsibility Score (CRS) for each recent edit:
//    - Higher for edits to files that appear in the error output.
//    - Higher for more recent edits (recency bias in causality).
//    - Higher for edits to files in the same package/directory.
// 4. Injects the top suspect step as guidance to review that specific
//    change first, preventing random/blind fix attempts.
//
// Design constraints:
//   - Zero LLM cost (deterministic matching + scoring).
//   - Fires at most 3 times per run (enough to guide, not spam).
//   - Non-blocking: guidance appended to result, execution proceeds.
//   - Caps edit log at 30 entries to bound memory.
//   - Only fires on actual failures (IsError or FAIL/error in output).

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// Max edit steps to keep in the causal trace
	causalMaxEdits = 30

	// Max warnings per run
	causalMaxWarnings = 3

	// How many recent edits to consider for attribution
	causalLookback = 10

	// CRS weights
	causalWtErrorFileMatch = 50 // edit target appears in error output
	causalWtRecency        = 10 // per-step recency (most recent = highest)
	causalWtSameDir        = 5  // same directory as error file
	causalWtSamePackage    = 8  // same Go package as error file

	// CRS threshold for "likely cause"
	causalSuspectThreshold = 25
)

// causalEditStep records a single mutation step in the agent trajectory.
type causalEditStep struct {
	iteration int    // which iteration in the agent loop
	toolName  string // edit_file, write_file, etc.
	filePath  string // primary target file
	dirPath   string // directory of target file
}

// causalAttributionState tracks edit steps and attributes failures to them.
type causalAttributionState struct {
	edits    []causalEditStep
	warnings int
}

func newCausalAttributionState() *causalAttributionState {
	return &causalAttributionState{
		edits: make([]causalEditStep, 0, causalMaxEdits),
	}
}

// editTools identifies which tool calls constitute "edit steps".
var causalEditTools = map[string]bool{
	"edit_file":       true,
	"write_file":      true,
	"multi_edit_file": true,
	"multi_file_edit": true,
	"file_ops":        true, // move/delete can also cause failures
}

// verifyToolPatterns identifies build/test verification commands.
var causalVerifyRe = regexp.MustCompile(`(?i)(go\s+(build|test|vet)|make\s+\w+|npm\s+(test|run)|cargo\s+(build|test)|pytest|jest|\.\/gradlew)`)

// errorFileRe extracts file paths from common build/test error output.
var causalErrorFileRe = regexp.MustCompile(`(?:^|\s)((?:\./)?[\w\-./]+\.go):(?:\d+)?:`)

// recordEdit logs a mutation step.
func (s *causalAttributionState) recordEdit(toolName, filePath string, iteration int) {
	if !causalEditTools[toolName] {
		return
	}
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return
	}

	step := causalEditStep{
		iteration: iteration,
		toolName:  toolName,
		filePath:  filePath,
		dirPath:   dirOfFile(filePath),
	}

	if len(s.edits) >= causalMaxEdits {
		// Drop oldest — sliding window
		s.edits = s.edits[1:]
	}
	s.edits = append(s.edits, step)
}

// dirOfFile extracts the directory portion of a file path.
func dirOfFile(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

// extractErrorFiles finds file paths referenced in build/test error output.
func extractErrorFiles(output string) []string {
	matches := causalErrorFileRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var files []string
	for _, m := range matches {
		// Normalize: strip leading ./ for consistent comparison
		f := strings.TrimSpace(m[1])
		f = strings.TrimPrefix(f, "./")
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	return files
}

// goPackageOf extracts the Go package path from a .go file path.
// e.g. "internal/agent/foo.go" → "internal/agent"
func goPackageOf(path string) string {
	if !strings.HasSuffix(path, ".go") {
		return ""
	}
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

// computeCRS computes the Causal Responsibility Score for an edit step
// given the error files extracted from the failure output.
func computeCRS(edit causalEditStep, errorFiles []string, recencyRank int) int {
	score := 0

	for _, ef := range errorFiles {
		// Exact file match — strongest signal
		if ef == edit.filePath || strings.HasSuffix(edit.filePath, ef) || strings.HasSuffix(ef, edit.filePath) {
			score += causalWtErrorFileMatch
			continue
		}

		// Same Go package
		ep := goPackageOf(ef)
		tp := goPackageOf(edit.filePath)
		if ep != "" && tp != "" && ep == tp {
			score += causalWtSamePackage
			continue
		}

		// Same directory
		ed := dirOfFile(ef)
		if ed != "" && ed == edit.dirPath {
			score += causalWtSameDir
		}
	}

	// Recency bonus: most recent edit gets the highest bonus
	score += recencyRank * causalWtRecency

	return score
}

// attributeFailure traces backward from a failure to identify the most
// likely causal edit step(s). Returns formatted guidance or "".
func (s *causalAttributionState) attributeFailure(output string) string {
	if s.warnings >= causalMaxWarnings {
		return ""
	}
	if len(s.edits) == 0 {
		return ""
	}

	// Only process if this looks like a build/test failure
	if !causalVerifyRe.MatchString(output) && !looksLikeFailure(output) {
		return ""
	}

	errorFiles := extractErrorFiles(output)

	// Score recent edits
	start := len(s.edits) - causalLookback
	if start < 0 {
		start = 0
	}
	recent := s.edits[start:]

	type scored struct {
		step  causalEditStep
		score int
		rank  int
	}

	var results []scored
	for i, edit := range recent {
		// Recency rank: most recent edit gets highest rank (i+1)
		recencyRank := i + 1
		score := computeCRS(edit, errorFiles, recencyRank)
		results = append(results, scored{step: edit, score: score, rank: i})
	}

	if len(results) == 0 {
		return ""
	}

	// Find top suspect
	best := results[0]
	for _, r := range results[1:] {
		if r.score > best.score {
			best = r
		}
	}

	// If no meaningful causal signal, skip
	if best.score < causalSuspectThreshold {
		return ""
	}

	s.warnings++

	// Format guidance
	var sb strings.Builder
	sb.WriteString("[causal-attribution] ")
	if best.score >= causalWtErrorFileMatch {
		sb.WriteString(fmt.Sprintf("Build/test failure likely caused by your %s to %s (step %d, CRS=%d — error output references this file). ",
			best.step.toolName, best.step.filePath, best.step.iteration, best.score))
	} else {
		sb.WriteString(fmt.Sprintf("Build/test failure most likely originated from your %s to %s (step %d, CRS=%d — same package/directory as error). ",
			best.step.toolName, best.step.filePath, best.step.iteration, best.score))
	}
	sb.WriteString("Review that change first before attempting a blind fix. ")
	sb.WriteString("Consider: git diff that file, check if the error line maps to your edit, or revert and re-verify.")

	return sb.String()
}

// looksLikeFailure detects common failure indicators in tool output.
func looksLikeFailure(output string) bool {
	outputLower := strings.ToLower(output)
	failPatterns := []string{
		"fail", "error:", "panic:", "undefined:", "cannot find",
		"compile error", "compilation error", "build failed", "test failed",
		"fatal error", "syntax error", "type mismatch",
	}
	for _, p := range failPatterns {
		if strings.Contains(outputLower, p) {
			return true
		}
	}
	return false
}

// reset clears state for a new run.
func (s *causalAttributionState) reset() {
	s.edits = s.edits[:0]
	s.warnings = 0
}
