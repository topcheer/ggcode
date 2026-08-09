package agent

// Fix Amnesia Detector - cross-file error pattern recurrence.
//
// Research basis:
//   - DAPLab/Columbia (Jan 2026): "9 Critical Failure Patterns of Coding Agents"
//     Pattern #9 (Exception & Error Handling) is the highest-impact failure:
//     agents "prioritize execution over correctness" and suppress errors rather
//     than internalizing the fix.
//   - SICA (arXiv:2504.15228, NeurIPS 2025): trajectory waste from repeated
//     mistakes that the agent already corrected earlier in the same run.
//   - Agent-R self-training: shows that learning from prior mistakes within a
//     trajectory significantly improves agent performance.
//
// What this detects:
//   The agent fixes a specific error category (e.g., nil dereference, missing
//   import, unused variable) in file A, then later in the same run writes or
//   edits file B introducing the SAME error pattern. This indicates the agent
//   treated the fix as a local patch rather than a generalizable lesson.
//
// How it works:
//   1. Tracks error categories that were OBSERVED (via tool failures, build
//      errors, test failures) and SUBSEQUENTLY FIXED in the same run.
//   2. When a write/edit introduces content matching a previously-fixed error
//      pattern in a DIFFERENT file, emits a guidance note.
//   3. Zero LLM cost — uses deterministic regex/classification heuristics.
//   4. Non-blocking — only injects guidance text.
//
// Distinct from:
//   - error_compounding: consecutive errors compounding (not cross-file recurrence)
//   - error_strategy_loop: same retry strategy repeated (not error pattern)
//   - error_regression: errors returning in the SAME file (not different file)
//   - history_error_accum: accumulation of errors (not pattern recurrence)

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// fixAmnesiaState tracks error categories fixed in the current run.
type fixAmnesiaState struct {
	mu sync.Mutex

	// fixedPatterns tracks error categories that were observed and fixed.
	// Key: error category; Value: file where it was fixed.
	fixedPatterns map[string][]string // category -> list of files

	// warned tracks whether we've already warned about this category.
	warned map[string]bool

	// maxWarnings caps total warnings per run.
	maxWarnings int
}

// faErrorCategory represents a classifiable error pattern.
type faErrorCategory struct {
	category string
	label    string
	// contentPattern matches content in newly written code that exhibits
	// this error pattern.
	contentPattern *regexp.Regexp
}

// Error patterns that agents commonly fix then re-introduce.
// These are checked against NEW file content after writes/edits.
var faErrorCategories = []faErrorCategory{
	{
		category: "nil-deref-after-nil-check",
		label:    "nil pointer used after nil check without guarding",
		// Pattern: if x != nil { ... } followed later by x.Method() outside the guard
		// Simplified: look for `err != nil` then bare usage of the value without nil check
		contentPattern: regexp.MustCompile(`(?m)(?:if\s+\w+\s*[,=]\s*\w+\s*!=\s*nil\s*\{[^}]*\}|err\s*!?=\s*nil[^{]*\{[^}]*\})[^}]*\.\w+\s*\(`),
	},
	{
		category: "unchecked-error",
		label:    "error return value ignored (discarded without checking)",
		// Looks for function calls that return errors but whose result is discarded
		contentPattern: regexp.MustCompile(`(?m)^\s*\w+\([^)]*\)\s*$`),
	},
	{
		category:       "missing-import",
		label:          "used a package symbol without importing the package",
		contentPattern: regexp.MustCompile(`(?m)(?:^|\s)(fmt|os|io|net|http|sync|time|errors|strings|strconv|bytes|context|encoding/json)\.\w+`),
	},
	{
		category:       "defer-in-loop",
		label:          "defer statement inside a loop (resource leak)",
		contentPattern: regexp.MustCompile(`(?m)for\s.*\{[^}]*defer\s+\w+\.`),
	},
	{
		category:       "range-val-copy",
		label:          "range loop uses value copy where address is needed",
		contentPattern: regexp.MustCompile(`(?m)for\s+_\s*,\s*\w+\s*:?=\s*range\s+\w+\s*\{[^}]*&\w+\}`),
	},
	{
		category:       "map-concurrent-write",
		label:          "map written without mutex in concurrent context",
		contentPattern: regexp.MustCompile(`(?m)go\s+func\s*\([^)]*\)\s*\{[^}]*\w+\[`),
	},
	{
		category:       "unclosed-resource",
		label:          "resource opened without defer-close",
		contentPattern: regexp.MustCompile(`(?m)(?:os\.Open|os\.Create|http\.Get|net\.Dial)\([^)]*\)`),
	},
}

// newFixAmnesiaState creates a fresh detector for this run.
func newFixAmnesiaState() *fixAmnesiaState {
	return &fixAmnesiaState{
		fixedPatterns: make(map[string][]string),
		warned:        make(map[string]bool),
		maxWarnings:   2,
	}
}

// recordErrorObserved tracks that an error of the given category was observed
// (from build output, test failures, or tool errors). Called when errors are
// seen in tool results.
func (d *fixAmnesiaState) recordErrorObserved(category, file string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// We only track it as "seen" — it becomes "fixed" when the agent edits
	// the file containing it (detected via recordFix).
	d.fixedPatterns[category] = appendIfMissing(d.fixedPatterns[category], file)
}

// recordFix marks a category as fixed in the given file (because the agent
// edited that file after the error was observed).
func (d *fixAmnesiaState) recordFix(category, file string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	files := d.fixedPatterns[category]
	for _, f := range files {
		if f == file {
			return // already tracked
		}
	}
	d.fixedPatterns[category] = append(d.fixedPatterns[category], file)
}

// checkNewContent examines newly written/edited file content for patterns
// that match previously-fixed error categories. Returns guidance text if
// fix amnesia is detected, empty string otherwise.
//
// fixFile is the file where the error was previously fixed.
// newFile is the file being written/edited now.
// content is the NEW content of newFile.
func (d *fixAmnesiaState) checkContentAgainstFixed(fixFile, newFile, content string) string {
	if fixFile == newFile {
		return "" // same file, not amnesia
	}
	if len(content) == 0 {
		return ""
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	warningCount := 0
	for cat := range d.warned {
		if d.warned[cat] {
			warningCount++
		}
	}

	var warnings []string
	for _, ec := range faErrorCategories {
		if d.warned[ec.category] {
			continue
		}
		// Was this category fixed in a different file?
		fixedFiles, ok := d.fixedPatterns[ec.category]
		if !ok {
			continue
		}

		fixedInDiffFile := false
		for _, f := range fixedFiles {
			if f != newFile && f != "" {
				fixedInDiffFile = true
				break
			}
		}
		if !fixedInDiffFile {
			continue
		}

		// Check if the new content matches the error pattern
		if ec.contentPattern.MatchString(content) {
			if warningCount+len(warnings) >= d.maxWarnings {
				break
			}
			d.warned[ec.category] = true
			msg := fmt.Sprintf("Fix Amnesia: you previously fixed '%s' in %s, but similar pattern detected in %s. "+
				"Apply the same fix proactively to avoid repeating the error.",
				ec.label, fixedFiles[0], newFile)
			warnings = append(warnings, msg)
		}
	}

	if len(warnings) == 0 {
		return ""
	}
	return "[fix-amnesia] " + strings.Join(warnings, " | ")
}

// classifyToolError extracts an error category from a tool error message.
// Returns empty string if no category matches.
func classifyToolError(_ string, errMsg string) (category, file string) {
	errLower := strings.ToLower(errMsg)
	if errLower == "" {
		return "", ""
	}

	// Extract file path from error if present
	file = extractFilePathFromError(errMsg)

	switch {
	case strings.Contains(errLower, "nil pointer") || strings.Contains(errLower, "nil dereference"):
		return "nil-deref-after-nil-check", file
	case strings.Contains(errLower, "declared and not used") || strings.Contains(errLower, "unused import") ||
		strings.Contains(errLower, "imported and not used"):
		return "missing-import", file
	case strings.Contains(errLower, "defer in loop") || strings.Contains(errLower, "resource leak"):
		return "defer-in-loop", file
	case strings.Contains(errLower, "concurrent map") || strings.Contains(errLower, "map write"):
		return "map-concurrent-write", file
	case strings.Contains(errLower, "range value copy") || strings.Contains(errLower, "took address of range"):
		return "range-val-copy", file
	}
	return "", ""
}

// extractFilePathFromError tries to extract a file path from an error message.
func extractFilePathFromError(errMsg string) string {
	// Look for common patterns: file.go, /path/to/file.go
	re := regexp.MustCompile(`(?:^|[\s:(])(/[^\s:]+\.(?:go|ts|js|py|rs))`)
	m := re.FindStringSubmatch(errMsg)
	if len(m) >= 2 {
		return m[1]
	}
	// Also try bare filename.go
	re2 := regexp.MustCompile(`([\w/]+\.go):\d+`)
	m2 := re2.FindStringSubmatch(errMsg)
	if len(m2) >= 2 {
		return m2[1]
	}
	return ""
}

// appendIfMissing adds s to slice only if not already present.
func appendIfMissing(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

// classifyBuildError classifies errors from build/test output.
// Returns the category and the file mentioned.
func classifyBuildError(output string) (category, file string) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cat, f := classifyToolError("build", line)
		if cat != "" {
			return cat, f
		}
	}
	return "", ""
}
