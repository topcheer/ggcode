package agent

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Edit Dry-Run Pre-Validation - fatal error detection BEFORE the write happens.
//
// Research basis: Claude Code and Cursor both perform pre-write validation
// that can block harmful edits before they touch disk. ggcode already has
// extensive POST-write integrity checks (write_integrity.go, 30+ checks),
// but these only fire AFTER the file has been modified. The agent then has
// to spend another iteration to fix the problem - wasting API calls, time,
// and triggering cascading recovery hooks.
//
// This module runs a FATAL-ONLY subset of the integrity checks on the
// proposed content (before safeExecute) and returns a blocking error if a
// guaranteed-failure condition is detected:
//
//   1. Go syntax errors - guaranteed `go build` failure
//   2. Binary corruption (null bytes) - corrupted file
//   3. Complete content loss - non-empty file becoming empty
//   4. Merge conflict markers - guaranteed build failure
//
// Non-fatal checks (style, patterns, potential issues) are intentionally
// excluded from the pre-write gate. These run post-write as warnings.
// Only conditions that are GUARANTEED to cause a build/runtime failure
// are blocked, minimizing false positives that could frustrate the agent.
//
// The gate is advisory: it returns an error result that stops the write,
// but the agent can retry with corrected content. This is strictly better
// than the current flow (write -> detect -> warn -> agent reads warning ->
// re-reads file -> re-edits), saving 2-3 iterations per fatal edit.

// dryRunValidate checks proposed file content for fatal errors before writing.
// Returns a non-empty string if a fatal issue is detected (write should be blocked).
// Returns empty string if the content passes fatal checks (write should proceed).
func dryRunValidate(filePath, oldContent, newContent string) string {
	var fatalErrors []string

	// 1. Go syntax errors - guaranteed build failure.
	if filepath.Ext(filePath) == ".go" && strings.TrimSpace(newContent) != "" {
		fset := token.NewFileSet()
		_, parseErr := parser.ParseFile(fset, filePath, newContent, 0)
		if parseErr != nil {
			if syntaxErrs := goSyntaxWarnings(filePath, parseErr); len(syntaxErrs) > 0 {
				maxReport := 2
				if len(syntaxErrs) < maxReport {
					maxReport = len(syntaxErrs)
				}
				for i := 0; i < maxReport; i++ {
					fatalErrors = append(fatalErrors, syntaxErrs[i])
				}
				if len(syntaxErrs) > maxReport {
					fatalErrors = append(fatalErrors,
						fmt.Sprintf("...and %d more syntax error(s)", len(syntaxErrs)-maxReport))
				}
			}
		}
	}

	// 2. Binary corruption - null bytes in text content.
	if strings.ContainsRune(newContent, 0) {
		count := strings.Count(newContent, "\x00")
		fatalErrors = append(fatalErrors,
			fmt.Sprintf("Content contains %d null byte(s) (\\x00) - file would be corrupted.", count))
	}

	// 3. Complete content loss - non-empty file becoming empty/whitespace.
	if strings.TrimSpace(oldContent) != "" && strings.TrimSpace(newContent) == "" {
		fatalErrors = append(fatalErrors,
			fmt.Sprintf("This edit would result in an EMPTY file (was %d bytes). "+
				"The old_text match likely consumed the entire file content.", len(oldContent)))
	}

	// 4. Merge conflict markers - guaranteed build failure in most languages.
	if marker := checkMergeConflictMarkers(filePath, newContent); marker != "" {
		fatalErrors = append(fatalErrors, marker)
	}

	if len(fatalErrors) == 0 {
		return ""
	}

	debug.Log("dryrun", "pre-write validation blocked %s: %d fatal issue(s)", filePath, len(fatalErrors))

	var b strings.Builder
	b.WriteString("[Edit blocked by pre-write validation]\n")
	b.WriteString("The following fatal issue(s) were detected BEFORE writing. ")
	b.WriteString("Fix these in your edit and retry - the file has NOT been modified.\n\n")
	for _, e := range fatalErrors {
		b.WriteString("  - ")
		b.WriteString(e)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// dryRunValidateBatch validates multiple proposed edits and returns a map
// from file path to fatal error message. Files not in the map passed validation.
func dryRunValidateBatch(plans []fileEditPlan) map[string]string {
	blockers := make(map[string]string)
	for _, p := range plans {
		if msg := dryRunValidate(p.Path, p.OldContent, p.NewContent); msg != "" {
			blockers[p.Path] = msg
		}
	}
	if len(blockers) > 0 {
		debug.Log("dryrun", "batch pre-write validation blocked %d/%d file(s)", len(blockers), len(plans))
	}
	return blockers
}

// fileEditPlan is a lightweight alias for tool.PlannedFileEdit to avoid
// importing the tool package in validation logic.
type fileEditPlan struct {
	Path       string
	OldContent string
	NewContent string
}
