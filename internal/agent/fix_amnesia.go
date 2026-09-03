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

	// observed tracks error categories seen in tool results, per file.
	// Key: error category; Value: files where the error was observed.
	observed map[string][]string

	// fixedPatterns tracks error categories that were observed AND
	// subsequently fixed (agent edited the file and the error did not recur).
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
		category: "missing-import",
		label:    "used a package symbol without importing the package",
		// #754: contentPattern nil -- detection is content-aware via
		// contentMatchesCategory (stdlib symbol used but import block lacks
		// the package). The old pattern matched ANY stdlib usage; healthy
		// code with correct imports hit ~100%.
	},
	{
		category: "unused-variable",
		label:    "variable declared and not used",
		// #754: no content pattern -- observation seeds only (see
		// classifyToolError). Using variables in new files is healthy code.
	},
	{
		category: "unused-import",
		label:    "import declared and not used",
		// #754: no content pattern -- the fix DELETES an import; new files
		// legitimately importing packages are healthy code.
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
}

// newFixAmnesiaState creates a fresh detector for this run.
func newFixAmnesiaState() *fixAmnesiaState {
	return &fixAmnesiaState{
		observed:      make(map[string][]string),
		fixedPatterns: make(map[string][]string),
		warned:        make(map[string]bool),
		maxWarnings:   2,
	}
}

// recordErrorObserved tracks that an error of the given category was observed
// (from build output, test failures, or tool errors). Called when errors are
// seen in tool results. #754: this only records OBSERVATION -- a category
// becomes "fixed" solely via recordFileEdited (agent edited the file after
// the error and the error did not recur on the same file).
func (d *fixAmnesiaState) recordErrorObserved(category, file string) {
	if category == "" || file == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.observed[category] = appendIfMissing(d.observed[category], file)
}

// recordFileEdited promotes OBSERVED categories for the edited file to
// FIXED: the agent touched the file after the error was seen (#754 wiring of
// the observe->fix two-phase design; recordFix was never called before).
func (d *fixAmnesiaState) recordFileEdited(file string) {
	if file == "" {
		return
	}
	// #1462-A: normalize BOTH sides - the observe side extracts compiler
	// output paths (repo-relative like internal/agent/foo.go), the edit
	// side passes absolute arg paths; the exact-string compare never
	// matched, so the observe->fixed promotion (this detector's MAIN
	// path) never fired and fixed patterns stayed forgotten.
	nf := normalizePath(file)
	d.mu.Lock()
	defer d.mu.Unlock()
	for cat, files := range d.observed {
		for _, f := range files {
			if normalizePath(f) == nf {
				d.fixedPatterns[cat] = appendIfMissing(d.fixedPatterns[cat], f)
			}
		}
	}
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

		// Check if the new content matches the error pattern (#754:
		// nil patterns and import-aware analysis handled inside).
		if contentMatchesCategory(ec, content) {
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
	// #754: "declared and not used" is an UNUSED VARIABLE -- fixing it (using
	// or removing the variable) has nothing to do with imports, and "unused
	// import" is fixed by DELETING an import. Neither implies that a later
	// file using stdlib symbols is missing imports. Split into its own
	// category with no contentPattern: it can seed observation (triggering
	// promotion via edit) but never matches new content, so it cannot
	// produce cross-file false positives on healthy code.
	case strings.Contains(errLower, "declared and not used"):
		return "unused-variable", file
	case strings.Contains(errLower, "imported and not used") || strings.Contains(errLower, "unused import"):
		return "unused-import", file
	case strings.Contains(errLower, "undefined:") && (strings.Contains(errLower, "fmt.") || strings.Contains(errLower, "os.") ||
		strings.Contains(errLower, "strings.") || strings.Contains(errLower, "errors.")):
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

// faStdlibPkgs lists the common stdlib packages checked by the
// missing-import content analysis (#754).
// Issue #1057: using full import paths (e.g., "net/http" not just "http").
var faStdlibPkgs = []string{"fmt", "os", "io", "io/ioutil", "net/http", "net", "sync", "time", "errors", "strings", "strconv", "bytes", "context", "encoding/json"}

// contentMatchesCategory reports whether new content exhibits the error
// category. Categories without a pattern (unused-variable/unused-import)
// never match; missing-import uses real import-block analysis instead of the
// old any-stdlib-usage pattern (#754).
func contentMatchesCategory(ec faErrorCategory, content string) bool {
	if ec.category == "missing-import" {
		return missingImportInContent(content)
	}
	if ec.contentPattern == nil {
		return false
	}
	return ec.contentPattern.MatchString(content)
}

// missingImportInContent reports whether the content uses one of the common
// stdlib packages without importing it. Handles both import forms:
// single-line `import "fmt"` and grouped `import ( ... )` blocks. Comments
// between imports are tolerated inside grouped blocks.
func missingImportInContent(content string) bool {
	for _, pkg := range faStdlibPkgs {
		used := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(pkg) + `\.`)
		if !used.MatchString(content) {
			continue
		}
		if !hasImportFor(content, pkg) {
			return true
		}
	}
	return false
}

// hasImportFor reports whether content imports pkg (single-line or block).
func hasImportFor(content, pkg string) bool {
	// #1462-B: the caller may feed a DIFF (edit/write tool results embed
	// '+ '/'- ' prefixed compactDiff lines) - anchor-tolerant variants
	// accept the diff prefix ahead of the line start.
	imp := regexp.MustCompile(`(?m)^[+\-]?\s*import\s+"` + regexp.QuoteMeta(pkg) + `"`)
	if imp.MatchString(content) {
		return true
	}
	blk := regexp.MustCompile(`(?s)import\s*\((.*?)\)`)
	for _, m := range blk.FindAllStringSubmatch(content, -1) {
		line := regexp.MustCompile(`(?m)^[+\-]?\s*(?:\w+\s+)?"` + regexp.QuoteMeta(pkg) + `"`)
		if line.MatchString(m[1]) {
			return true
		}
	}
	return false
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
