package tool

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/security"
)

// DiffIssue represents a quality issue found in a staged diff.
type DiffIssue struct {
	File     string `json:"file"`
	Line     int    `json:"line"`     // line number in the new file
	Severity string `json:"severity"` // "warning" or "info"
	Category string `json:"category"` // "debug-stmt", "todo", "merge-conflict", "secret", "debugger"
	Message  string `json:"message"`
}

// maxDiffScanIssues caps the number of issues reported to avoid overwhelming
// the agent context when a large diff triggers many matches.
const maxDiffScanIssues = 15

// debugStmtPatterns matches common debug/print statements across languages.
// Only checked against ADDED lines. Test files are excluded separately.
var debugStmtPatterns = []*regexp.Regexp{
	// Go
	regexp.MustCompile(`\bfmt\.Println\(`),
	regexp.MustCompile(`\bfmt\.Printf\(`),
	regexp.MustCompile(`\bprintln\(`),
	// JavaScript/TypeScript
	regexp.MustCompile(`\bconsole\.log\(`),
	regexp.MustCompile(`\bconsole\.debug\(`),
	// Python
	regexp.MustCompile(`\bprint\(`),
	regexp.MustCompile(`\bpprint\.pprint\(`),
	regexp.MustCompile(`\bpprint\.pformat\(`),
	// Ruby
	regexp.MustCompile(`\bbinding\.pry\b`),
	regexp.MustCompile(`\bbinding\.irb\b`),
	// Java/Kotlin/Scala
	regexp.MustCompile(`\bSystem\.out\.println\(`),
	regexp.MustCompile(`\bSystem\.err\.println\(`),
	// Rust
	regexp.MustCompile(`\beprintln!\(`),
	regexp.MustCompile(`\bdbg!\(`),
	// PHP
	regexp.MustCompile(`\bvar_dump\(`),
	regexp.MustCompile(`\bprint_r\(`),
	regexp.MustCompile(`\bdd\(`),
	// C/C++
	regexp.MustCompile(`\bfprintf\s*\(\s*stderr\b`),
}

// debuggerPatterns matches debugger/breakpoint statements.
var debuggerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bpdb\.set_trace\(\)`),
	regexp.MustCompile(`\bbreakpoint\(\)`),
	regexp.MustCompile(`^\s*import\s+pdb\b`),
	regexp.MustCompile(`^\s*debugger;\s*$`),
	regexp.MustCompile(`\bbinding\.pry\b`),
}

// todoPattern matches TODO/FIXME/HACK/XXX in added lines.
var todoPattern = regexp.MustCompile(`(?i)\b(TODO|FIXME|HACK|XXX|BUG|WORKAROUND)\b`)

// mergeConflictMarkers matches unresolved merge conflict markers.
var mergeConflictMarkers = []*regexp.Regexp{
	regexp.MustCompile(`^<{7}`),
	regexp.MustCompile(`^={7}`),
	regexp.MustCompile(`^>{7}`),
	regexp.MustCompile(`^\|{7}`),
}

// testFilePattern matches common test file naming conventions so we can
// exclude them from debug-statement checks (print statements are expected
// in test files).
var testFilePattern = regexp.MustCompile(`(_test\.go|\.test\.[jt]sx?|\.spec\.[jt]sx?|test_.*\.py|.*_test\.py|\.test\.ts|\.test\.js|__tests__/.*|Test\.cs|_spec\.rb|\.spec\.rb|_test\.rs)$`)

// diffFileHeader matches the "diff --git a/file b/file" or "+++ b/file" lines
// in unified diff output to track the current file.
var diffFileHeader = regexp.MustCompile(`^\+\+\+\s+b/(.+)`)
var diffOldFileHeader = regexp.MustCompile(`^---\s+a/(.+)`)
var diffHunkHeader = regexp.MustCompile(`^@@\s+-\d+(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@`)

// ScanStagedDiffForIssues analyzes a unified diff (e.g. from "git diff --cached")
// and returns quality issues found in ADDED lines only. This provides a
// deterministic pre-commit quality check that catches common mistakes before
// they enter version control.
//
// Checked categories:
//   - Debug statements (fmt.Println, console.log, print(), etc.)
//   - Debugger/breakpoint statements (pdb.set_trace, debugger;, binding.pry)
//   - TODO/FIXME/HACK/XXX markers in added lines
//   - Unresolved merge conflict markers
//   - Potential secrets (API keys, tokens, passwords)
//
// Test files are excluded from debug-statement checks since print/debug
// output is expected in tests. Secrets and merge conflicts are always checked.
func ScanStagedDiffForIssues(diffOutput string) []DiffIssue {
	if strings.TrimSpace(diffOutput) == "" {
		return nil
	}

	var issues []DiffIssue
	currentFile := ""
	newLineNum := 0
	isTestFile := false

	lines := strings.Split(diffOutput, "\n")

	for _, line := range lines {
		// Track current file from "+++ b/file" headers.
		if m := diffFileHeader.FindStringSubmatch(line); m != nil {
			currentFile = m[1]
			isTestFile = testFilePattern.MatchString(currentFile)
			continue
		}
		// Also track old file header for deletion-only changes.
		if diffOldFileHeader.MatchString(line) {
			continue
		}

		// Track line numbers from hunk headers: @@ -old,count +new,count @@
		if m := diffHunkHeader.FindStringSubmatch(line); m != nil {
			fmt.Sscanf(m[1], "%d", &newLineNum)
			continue
		}

		// Only check ADDED lines (lines starting with '+', not "+++").
		if len(line) == 0 || line[0] != '+' {
			// Context lines and removed lines advance the new line counter
			// for context lines (' '), removed lines ('-') do not.
			if len(line) > 0 && line[0] == ' ' {
				newLineNum++
			}
			continue
		}

		addedContent := line[1:] // strip the leading '+'
		issueLine := newLineNum  // this added line's number in the new file
		newLineNum++             // advance for the next line

		if len(issues) >= maxDiffScanIssues {
			break
		}

		// --- Always-checked categories ---

		// 1. Merge conflict markers (critical — should never be committed).
		for _, pat := range mergeConflictMarkers {
			if pat.MatchString(addedContent) {
				issues = append(issues, DiffIssue{
					File:     currentFile,
					Line:     issueLine,
					Severity: "warning",
					Category: "merge-conflict",
					Message:  "Unresolved merge conflict marker — resolve before committing.",
				})
				break
			}
		}

		// 2. Secrets (always check, even in test files).
		if secretScanEnabled {
			findings := security.ScanForSecrets(currentFile, addedContent+"\n")
			for _, f := range findings {
				if len(issues) >= maxDiffScanIssues {
					break
				}
				issues = append(issues, DiffIssue{
					File:     currentFile,
					Line:     issueLine,
					Severity: severityFromSecurity(f.Severity),
					Category: "secret",
					Message:  fmt.Sprintf("Potential secret detected: %s. Remove or use an environment variable.", f.Name),
				})
			}
		}

		// --- Source-file-only categories (skip test files) ---

		if isTestFile {
			continue
		}

		// 3. Debugger/breakpoint statements.
		for _, pat := range debuggerPatterns {
			if pat.MatchString(addedContent) {
				issues = append(issues, DiffIssue{
					File:     currentFile,
					Line:     issueLine,
					Severity: "warning",
					Category: "debugger",
					Message:  "Debugger/breakpoint statement detected — remove before committing.",
				})
				break
			}
		}

		// 4. Debug print statements.
		for _, pat := range debugStmtPatterns {
			if pat.MatchString(addedContent) {
				issues = append(issues, DiffIssue{
					File:     currentFile,
					Line:     issueLine,
					Severity: "info",
					Category: "debug-stmt",
					Message:  "Debug print statement detected — remove if not intentionally logging.",
				})
				break
			}
		}

		// 5. TODO/FIXME/HACK/XXX markers in added lines.
		if todoPattern.MatchString(addedContent) {
			issues = append(issues, DiffIssue{
				File:     currentFile,
				Line:     issueLine,
				Severity: "info",
				Category: "todo",
				Message:  "TODO/FIXME marker in new code — track this as a follow-up task.",
			})
		}
	}

	return issues
}

// severityFromSecurity maps the security package's severity levels to
// diff scan severity levels.
func severityFromSecurity(sec string) string {
	if sec == "high" {
		return "warning"
	}
	return "info"
}

// FormatDiffIssues formats a list of DiffIssues into a human-readable warning
// string suitable for appending to a git_commit result.
func FormatDiffIssues(issues []DiffIssue) string {
	if len(issues) == 0 {
		return ""
	}

	var b strings.Builder
	warnings := 0
	infos := 0
	for _, iss := range issues {
		if iss.Severity == "warning" {
			warnings++
		} else {
			infos++
		}
	}

	// Header line summarizing the scan results.
	b.WriteString("[Pre-commit scan] ")
	parts := []string{}
	if warnings > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", warnings))
	}
	if infos > 0 {
		parts = append(parts, fmt.Sprintf("%d info", infos))
	}
	b.WriteString(strings.Join(parts, ", "))
	if warnings > 0 {
		b.WriteString(" — review before pushing")
	}
	b.WriteString(":\n")

	for _, iss := range issues {
		loc := iss.File
		if iss.Line > 0 {
			loc = fmt.Sprintf("%s:%d", iss.File, iss.Line)
		}
		icon := "i"
		if iss.Severity == "warning" {
			icon = "!"
		}
		b.WriteString(fmt.Sprintf("  [%s] %s — %s\n", icon, loc, iss.Message))
	}

	if warnings > 0 {
		b.WriteString("\nFix the warnings above before pushing to a shared branch.")
	}

	return strings.TrimRight(b.String(), "\n")
}

// getStagedDiff runs "git diff --cached" in the given directory and returns
// the output. Returns empty string if git fails or there are no staged changes.
func getStagedDiff(ctx context.Context, dir string) string {
	cmd := gitCommand(ctx, "diff", "--cached")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}
