package agent

// Error Strategy Loop Detector -- Procedural Memory Failure
//
// Research basis: ProcMEM (arXiv:2602.01869, Feb 2026) demonstrates that
// LLM agents "re-derive solutions even in recurring scenarios" causing
// "computational redundancy and execution instability." The paper introduces
// procedural memory -- encoding reusable skills from experience so the agent
// doesn't repeat the same failed approaches. The core insight: agents that
// don't leverage within-run experience keep hitting the same wall.
//
// Distinction from existing detectors:
//   - recurring_error.go: detects same BUILD/TEST error fingerprint recurring
//     across edit cycles (content-level fingerprint matching)
//   - correction_spiral.go: error SEVERITY escalation (things getting worse)
//   - edit_fail_recovery.go: single edit failure → recovery guidance
//   - fix_cascade.go: edit→verify→fail cycle with DIFFERENT errors
//
// THIS detector fills the orthogonal gap: detects when the same broad error
// CATEGORY (file-not-found, old-text-mismatch, timeout, permission, etc.)
// recurs across potentially DIFFERENT tools and files. This indicates a
// SYSTEMIC approach failure -- the agent's strategy is wrong, not just one
// edit. Example: agent keeps getting "file not found" on different files,
// meaning it never verified paths exist before editing.
//
// Design:
//   - Zero LLM cost -- pure error classification + recurrence counting
//   - Fires at most 2 times per run
//   - Non-blocking: hint injected as user message, agent continues
//   - Threshold: 3+ same-category errors within a sliding window of 15

import (
	"strings"
)

const (
	errStrategyThreshold   = 3
	errStrategyWindow      = 15
	maxErrStrategyWarnings = 2
)

type errCategory string

const (
	errCatFileNotFound    errCategory = "file-not-found"
	errCatOldTextMismatch errCategory = "old-text-mismatch"
	errCatTimeout         errCategory = "timeout"
	errCatPermission      errCategory = "permission"
	errCatGeneric         errCategory = "generic"
)

type errStrategyState struct {
	recentResults []bool // isError flags in window order
	recentCats    []errCategory
	catCounts     map[errCategory]int
	warningCount  int
	firedFor      map[errCategory]bool
}

func newErrStrategyState() *errStrategyState {
	return &errStrategyState{
		recentResults: make([]bool, 0, errStrategyWindow+1),
		recentCats:    make([]errCategory, 0, errStrategyWindow+1),
		catCounts:     make(map[errCategory]int),
		firedFor:      make(map[errCategory]bool),
	}
}

func (s *errStrategyState) reset() {
	s.recentResults = s.recentResults[:0]
	s.recentCats = s.recentCats[:0]
	s.catCounts = make(map[errCategory]int)
	s.warningCount = 0
	s.firedFor = make(map[errCategory]bool)
}

// classifyErrResult categorizes a tool result into an error category.
// Returns the category and true if it's an error, false otherwise.
func classifyErrResult(resultContent string, isError bool) (errCategory, bool) {
	if !isError {
		// #340: only isError=true results count as error evidence. Successful
		// outputs routinely contain "error"/"not found" substrings (source code,
		// "0 errors" vet output, other detectors' guidance text), and counting
		// them turned "3 same-category errors" into "any 3 error-worded results".
		return errCatGeneric, false
	}
	lower := strings.ToLower(resultContent)
	return classifyByContent(lower), true
}

func classifyByContent(lower string) errCategory {
	switch {
	case strings.Contains(lower, "old_text") &&
		(strings.Contains(lower, "not found") || strings.Contains(lower, "not unique") || strings.Contains(lower, "does not match")):
		return errCatOldTextMismatch
	case strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "file not found") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "cannot find") ||
		(strings.Contains(lower, "not found") && (strings.Contains(lower, "path") || strings.Contains(lower, "file"))):
		return errCatFileNotFound
	case strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "deadline exceeded"):
		return errCatTimeout
	case strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "operation not permitted"):
		return errCatPermission
	}
	return errCatGeneric
}

func (s *errStrategyState) recordResult(resultContent string, isError bool) {
	cat, isErr := classifyErrResult(resultContent, isError)
	s.pushEntry(cat, isErr)
}

func (s *errStrategyState) pushEntry(cat errCategory, isErr bool) {
	s.recentResults = append(s.recentResults, isErr)
	s.recentCats = append(s.recentCats, cat)
	if len(s.recentResults) > errStrategyWindow {
		if s.recentResults[0] {
			s.catCounts[s.recentCats[0]]--
			if s.catCounts[s.recentCats[0]] <= 0 {
				delete(s.catCounts, s.recentCats[0])
			}
		}
		s.recentResults = s.recentResults[1:]
		s.recentCats = s.recentCats[1:]
	}
	if isErr {
		s.catCounts[cat]++
	}
}

// checkAndWarn returns guidance if a recurring error pattern is detected.
func (s *errStrategyState) checkAndWarn() string {
	if s.warningCount >= maxErrStrategyWarnings {
		return ""
	}
	var dominantCat errCategory
	var dominantCount int
	for c, n := range s.catCounts {
		if n > dominantCount {
			dominantCount = n
			dominantCat = c
		}
	}
	if dominantCount < errStrategyThreshold || s.firedFor[dominantCat] {
		return ""
	}
	// #340: generic has no semantic identity — "the same error category is
	// recurring" is not a valid claim for a catch-all bucket of unrelated
	// one-off errors. Require a stronger threshold for it.
	if dominantCat == errCatGeneric && dominantCount < 5 {
		return ""
	}
	s.firedFor[dominantCat] = true
	s.warningCount++
	return formatErrStrategyWarning(dominantCat, dominantCount)
}

func formatErrStrategyWarning(cat errCategory, count int) string {
	var label, strategy string
	switch cat {
	case errCatFileNotFound:
		label = "file-not-found"
		strategy = "Use glob or list_directory to discover actual file paths before editing. Files may have been renamed or your assumed path is wrong."
	case errCatOldTextMismatch:
		label = "old-text-mismatch"
		strategy = "Re-read the target file to get its current exact content before editing. Your cached version may be stale."
	case errCatTimeout:
		label = "timeout"
		strategy = "The operation is timing out. Reduce scope, add pagination, or split into smaller operations."
	case errCatPermission:
		label = "permission"
		strategy = "Permission errors indicate filesystem or access constraints. Verify the path is writable."
	default:
		label = "generic"
		strategy = "You are hitting multiple unrelated one-off errors. Slow down: re-read the relevant docs/files before each tool call and verify arguments before executing."
	}
	if cat == errCatGeneric {
		return "[error-strategy-loop] Detected " + errStrategyItoa(count) + " unrelated errors in recent tool calls. " +
			"Strategy change: " + strategy
	}
	return "[error-strategy-loop] Detected " + errStrategyItoa(count) + " '" + label +
		"' errors in recent tool calls. The same error category is recurring -- " +
		"your approach is not working. Strategy change: " + strategy
}

// itoa is a local int-to-string to avoid fmt import overhead.
func errStrategyItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
