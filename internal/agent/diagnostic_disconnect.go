package agent

// Diagnostic-Action Disconnect Detector
//
// Research basis: SWE-bench trajectory analysis (SWE-Search, ICLR 2025) and
// AgentDebug (arXiv:2509.25370) found that unsuccessful agent runs are
// characterized by agents receiving rich diagnostic information (compilation
// errors, test failures, runtime exceptions) but then taking actions that do
// not address the diagnostics -- "never testing whether the patch actually
// worked, and never considering that the root cause might be elsewhere."
//
// The backtracking replanning literature (2025-2026) identifies this as
// "contradiction detection": detecting when a step's output contradicts an
// assumption in the remaining plan, and triggering replanning proactively
// rather than waiting for failure. Agents without this detection continue
// down their original path even when diagnostic feedback invalidates their
// approach.
//
// Concrete pattern this detects:
//
//   Iteration 5: edit_file foo.go       → error: undefined: NewWidget
//   Iteration 6: edit_file bar.go       → success (but unrelated to the error)
//   Iteration 7: edit_file baz.go       → success (still not fixing the error)
//   Iteration 8: read_file qux.go       → success (reading, not fixing)
//
// The agent received a clear diagnostic ("undefined: NewWidget") at iteration 5
// but its subsequent 3 actions (6, 7, 8) don't reference or fix the issue.
// This is the "diagnostic disconnect" -- actionable feedback is received but
// not acted upon.
//
// Distinction from existing detectors:
//   - recurring_error.go: detects SAME error returning after edits (error-side)
//   - fix_cascade.go: detects consecutive error FIX attempts failing (fix-side)
//   - compounding_failure.go: tracks cross-tool failure RATE (aggregate)
//   - silent_error.go: detects ignoring errors entirely (error-side)
//   - THIS: detects RECEIVING diagnostics then PIVOTING AWAY (action-side)
//
// The key insight: the agent DID receive the diagnostic (unlike silent_error),
// and it's NOT making the same error recur (unlike recurring_error), and
// it's NOT failing on everything (unlike compounding_failure). Instead, it's
// SUCCEEDING at unrelated actions while ignoring a specific diagnostic -- the
// most insidious form of trajectory deviation because individual actions
// appear productive.
//
// Approach: track diagnostic keywords from failed tool results and count
// subsequent tool calls that are NOT edits to the file(s) mentioned in the
// diagnostic. After N actions without addressing the diagnostic, inject
// guidance. Zero LLM cost.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// ddMaxDisconnect: maximum number of tool calls that don't address a known
	// diagnostic before triggering guidance.
	ddMaxDisconnect = 4

	// ddMaxWarnings: cap on guidance injections per run (advisory, non-blocking).
	ddMaxWarnings = 2

	// ddDiagnosticDisplayLen: max length of diagnostic snippet in messages.
	ddDiagnosticDisplayLen = 80

	// ddFilePathDisplayLen: max length of file path in messages.
	ddFilePathDisplayLen = 60
)

// ddDiagnostic captures a single diagnostic signal from a failed tool result.
type ddDiagnostic struct {
	// keyword: the normalized diagnostic keyword (e.g., "undefined: NewWidget").
	keyword string

	// sourceFile: the file the diagnostic points to (extracted from error or
	// the tool's target file). Empty if no file could be identified.
	sourceFile string

	// rawSnippet: a short snippet of the diagnostic for display.
	rawSnippet string

	// iteration: when the diagnostic was observed.
	iteration int

	// disconnectCount: how many subsequent tool calls didn't address this.
	disconnectCount int

	// addressed: true once the agent edits the source file (or a related file).
	addressed bool
}

// diagnosticDisconnectState tracks diagnostics and whether subsequent actions
// address them.
type diagnosticDisconnectState struct {
	// activeDiagnostics: diagnostics being tracked (not yet addressed).
	activeDiagnostics []*ddDiagnostic

	// warningCount: how many times guidance has been injected this run.
	warningCount int

	// lastIteration: the most recent iteration number seen.
	lastIteration int
}

func newDiagnosticDisconnectState() *diagnosticDisconnectState {
	return &diagnosticDisconnectState{}
}

func (d *diagnosticDisconnectState) reset() {
	d.activeDiagnostics = nil
	d.warningCount = 0
	d.lastIteration = 0
}

// ddFileRe extracts file paths from diagnostic messages.
var ddFileRe = regexp.MustCompile(`([a-zA-Z0-9_./\-]+\.(?:go|py|ts|tsx|js|jsx|rs|java|rb|c|cpp|h|hpp|cs|php|swift|kt|scala))`)

// ddDiagnosticRe identifies lines containing actionable diagnostic keywords.
var ddDiagnosticRe = regexp.MustCompile(`(?i)(undefined|undeclared|not defined|cannot find|does not exist|no such file|cannot resolve|unresolved|not found|expected|missing|required|unknown type|cannot assign|incompatible|mismatch|syntax error|panic|nil pointer|exception|traceback)`)

// ddErrorResultRe is currently unused -- ddDiagnosticRe covers the detection.
// Kept as documentation of the pattern for future refinement.

// recordToolResult processes a completed tool call. If the result contains
// diagnostic content, a new diagnostic is registered. If the tool is a file
// edit to a tracked diagnostic's source file, that diagnostic is marked addressed.
func (d *diagnosticDisconnectState) recordToolResult(toolName, targetFile, result string, iteration int) {
	d.lastIteration = iteration

	// Check if this tool call addresses any active diagnostic (file edit to source).
	isEdit := editTools[toolName]
	if isEdit && targetFile != "" {
		for _, diag := range d.activeDiagnostics {
			if !diag.addressed && fileMatches(targetFile, diag.sourceFile) {
				diag.addressed = true
				debug.Log("diagnostic-disconnect",
					"diagnostic '%s' addressed by edit to %s at iteration %d",
					diag.keyword, shortPath(targetFile, ddFilePathDisplayLen), iteration)
			}
		}
	}

	// Check if the result contains new diagnostic content.
	if !ddDiagnosticRe.MatchString(result) {
		return
	}

	diag := extractDiagnostic(targetFile, result, iteration)
	if diag == nil {
		return
	}

	// Avoid duplicate diagnostics for the same keyword.
	for _, existing := range d.activeDiagnostics {
		if existing.keyword == diag.keyword && !existing.addressed {
			return // Already tracking this diagnostic.
		}
	}

	d.activeDiagnostics = append(d.activeDiagnostics, diag)

	debug.Log("diagnostic-disconnect",
		"new diagnostic registered: '%s' in %s at iteration %d",
		diag.keyword, shortPath(diag.sourceFile, ddFilePathDisplayLen), iteration)
}

// recordAction marks that a tool call happened, incrementing disconnect counters
// for un-addressed diagnostics. This should be called for EVERY tool call,
// including non-edit tools.
func (d *diagnosticDisconnectState) recordAction(_ int) {
	for _, diag := range d.activeDiagnostics {
		if !diag.addressed {
			diag.disconnectCount++
		}
	}
}

// check evaluates whether any diagnostic has been ignored for too long and
// returns guidance if so. Returns empty string if no guidance needed.
func (d *diagnosticDisconnectState) check() string {
	if d.warningCount >= ddMaxWarnings {
		return ""
	}

	// Find the most severely disconnected diagnostic.
	var worst *ddDiagnostic
	for _, diag := range d.activeDiagnostics {
		if diag.addressed {
			continue
		}
		if diag.disconnectCount >= ddMaxDisconnect {
			if worst == nil || diag.disconnectCount > worst.disconnectCount {
				worst = diag
			}
		}
	}

	if worst == nil {
		return ""
	}

	d.warningCount++
	worst.addressed = true // Mark as addressed so we don't re-fire for the same diagnostic.

	debug.Log("diagnostic-disconnect",
		"guidance injected: diagnostic '%s' ignored for %d actions since iteration %d (warning %d/%d)",
		worst.keyword, worst.disconnectCount, worst.iteration, d.warningCount, ddMaxWarnings)

	return fmt.Sprintf(
		"[diagnostic disconnect] A diagnostic signal from iteration %d has not been addressed in %d subsequent tool calls:\n"+
			"  Issue: %s\n"+
			"  Source: %s\n\n"+
			"Your recent actions appear productive but are NOT addressing this known issue.\n"+
			"1. Directly fix the diagnostic above before proceeding with other work\n"+
			"2. If the fix requires changes elsewhere, trace the dependency chain from the error site\n"+
			"3. If this diagnostic is stale (already resolved), verify with a build/test run\n"+
			"4. Do NOT continue with unrelated changes while this error remains unaddressed",
		worst.iteration, worst.disconnectCount,
		worst.rawSnippet,
		shortPath(worst.sourceFile, ddFilePathDisplayLen),
	)
}

// extractDiagnostic parses a tool result for actionable diagnostic content.
// Returns nil if no meaningful diagnostic can be extracted.
func extractDiagnostic(targetFile, result string, iteration int) *ddDiagnostic {
	lines := strings.Split(result, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || len(line) < 10 {
			continue
		}

		if !ddDiagnosticRe.MatchString(line) {
			continue
		}

		// Found a diagnostic line. Extract the keyword (first matching pattern).
		keyword := normalizeDiagnostic(line)
		if keyword == "" {
			continue
		}

		// Try to extract the source file from the diagnostic line.
		sourceFile := targetFile
		if matches := ddFileRe.FindStringSubmatch(line); len(matches) > 0 {
			sourceFile = matches[1]
		}

		return &ddDiagnostic{
			keyword:    keyword,
			sourceFile: sourceFile,
			rawSnippet: truncateDD(line, ddDiagnosticDisplayLen),
			iteration:  iteration,
		}
	}

	return nil
}

// normalizeDiagnostic extracts a normalized keyword from a diagnostic line.
func normalizeDiagnostic(line string) string {
	// Try to extract the key diagnostic phrase.
	for _, pat := range []string{
		`undefined:\s*\S+`,
		`undeclared:\s*\S+`,
		`not defined:\s*\S+`,
		`cannot find[^.]*`,
		`does not exist[^.]*`,
		`no such file[^.]*`,
		`cannot resolve[^.]*`,
		`unresolved[^.]*`,
		`not found[^.]*`,
		`expected[^.]*`,
		`missing[^.]*`,
		`required[^.]*`,
		`unknown type[^.]*`,
		`cannot assign[^.]*`,
		`incompatible[^.]*`,
		`mismatch[^.]*`,
		`syntax error[^.]*`,
		`panic.*`,
		`nil pointer.*`,
		`exception.*`,
		`traceback.*`,
	} {
		re := regexp.MustCompile(`(?i)` + pat)
		if match := re.FindString(line); match != "" {
			return strings.TrimSpace(match)
		}
	}
	return ""
}

// fileMatches checks if targetFile matches or is closely related to sourceFile.
func fileMatches(targetFile, sourceFile string) bool {
	if targetFile == "" || sourceFile == "" {
		return false
	}
	// Direct match or suffix match (handles relative vs absolute paths).
	if targetFile == sourceFile {
		return true
	}
	if strings.HasSuffix(targetFile, sourceFile) || strings.HasSuffix(sourceFile, targetFile) {
		return true
	}
	// Same basename (e.g., foo.go edited in different directory structure).
	targetBase := targetFile
	if idx := strings.LastIndex(targetFile, "/"); idx >= 0 {
		targetBase = targetFile[idx+1:]
	}
	sourceBase := sourceFile
	if idx := strings.LastIndex(sourceFile, "/"); idx >= 0 {
		sourceBase = sourceFile[idx+1:]
	}
	return targetBase == sourceBase && targetBase != ""
}

// truncateDD shortens a string for display, appending "..." if truncated.
func truncateDD(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// shortPath truncates a file path for display.
func shortPath(path string, maxLen int) string {
	if path == "" {
		return "(unknown)"
	}
	return truncateDD(path, maxLen)
}
