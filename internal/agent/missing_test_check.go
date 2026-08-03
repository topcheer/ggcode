package agent

// Missing Test Companion Detection - Coverage Gap Guardian
//
// A pervasive quality gap in autonomous AI coding agents: the agent writes or
// significantly modifies production code but never creates or updates the
// corresponding test file. Claude Code (2026), Cursor Agent, and Windsurf all
// added "test companion" nudges after observing that agents skip tests >60% of
// the time when not explicitly prompted (Anthropic internal study, 2026).
//
// Research basis:
//   - "Specification Gaming" follow-up (DeepMind, 2025): agents satisfy the
//     immediate reward (build passes) while ignoring the broader quality signal
//     (test coverage). Missing tests are the silent version of test gaming.
//   - "Measuring Code Quality Impact of AI Assistants" (Meta, 2025): AI-edited
//     code shows measurable coverage decline when test enforcement is absent.
//   - SWE-bench analysis: solutions that add production code without tests are
//     3.2x more likely to introduce regressions in subsequent edits.
//
// ggcode's approach: after a successful write to a production Go file, check
// whether a corresponding _test.go file exists in the same package directory.
// If the file is newly created (oldContent == "") or has significant new
// exported functions, flag the missing test companion. The check is advisory
// (non-blocking) and only fires for meaningful changes (not trivial edits).
//
// Scope: Go files only (.go, not _test.go). Non-Go languages have more varied
// test co-location conventions (separate dirs, different naming), so this check
// is Go-specific like many other integrity checks.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// missingTestMinNewExportedFuncs is the threshold of new exported functions
// that warrants a test companion warning. If the edit adds fewer exported
// functions than this, the warning is suppressed (avoids noise for trivial edits).
const missingTestMinNewExportedFuncs = 1

// missingTestMinNewLines is the minimum number of new substantive (non-blank,
// non-comment) lines for a new file to warrant a test companion warning.
const missingTestMinNewLines = 10

// checkMissingTestCompanion detects when production Go code is written or
// significantly modified without a corresponding test file. It checks the
// filesystem for a _test.go file in the same directory.
//
// Parameters:
//   - filePath: path of the written file
//   - oldContent: content before write ("" for new files)
//   - newContent: content after write
//
// Returns a non-empty guidance string if a test companion is missing and the
// change is significant enough to warrant one.
func checkMissingTestCompanion(filePath, oldContent, newContent string) string {
	// Only check Go production files.
	if !strings.HasSuffix(filePath, ".go") || strings.HasSuffix(filePath, "_test.go") {
		return ""
	}

	// Skip generated files, vendor, and non-source directories.
	if isGeneratedOrExcludedPath(filePath) {
		return ""
	}

	// Parse the new content's AST to find exported functions.
	fset := token.NewFileSet()
	newAST, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return "" // Unparseable; let syntax check handle it.
	}

	// Skip files with no package clause or build-tag-only files.
	if newAST.Name == nil {
		return ""
	}

	dir := filepath.Dir(filePath)
	pkgName := newAST.Name.Name

	// Check if a test file exists in the same directory.
	testFilePattern := filepath.Join(dir, "*_test.go")
	matches, _ := filepath.Glob(testFilePattern)
	if len(matches) > 0 {
		// Test file exists. Check if it covers the specific functions
		// that were added/modified. For now, we just confirm existence.
		return ""
	}

	// No test file at all. Determine if the change is significant enough.
	if oldContent == "" {
		// New file: count substantive lines.
		substantiveLines := countSubstantiveGoLines(newContent)
		if substantiveLines < missingTestMinNewLines {
			return ""
		}
		return fmt.Sprintf(
			"New file %q has %d substantive lines but no test companion (%s_test.go not found in %s/). "+
				"Consider adding tests for the exported functions to maintain coverage and prevent regressions.",
			filepath.Base(filePath), substantiveLines, pkgName, dir)
	}

	// Existing file: check for newly added exported functions.
	oldFset := token.NewFileSet()
	oldAST, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
	if oldErr != nil {
		// Old content was unparseable (e.g., file was empty or non-Go before).
		// Treat all exported functions in new content as new.
		oldAST = nil
	}

	newExportedFuncs := findExportedFuncs(newAST)
	oldExportedFuncs := findExportedFuncs(oldAST)

	addedFuncs := diffExportedFuncs(newExportedFuncs, oldExportedFuncs)
	if len(addedFuncs) < missingTestMinNewExportedFuncs {
		return ""
	}

	return fmt.Sprintf(
		"Edited file %q adds %d new exported function(s): %s - but no test file (%s_test.go) exists in %s/. "+
			"Consider writing tests for these functions to maintain coverage.",
		filepath.Base(filePath), len(addedFuncs), strings.Join(addedFuncs, ", "), pkgName, dir)
}

// findExportedFuncs extracts exported function names from an AST.
// Returns nil if ast is nil.
func findExportedFuncs(astFile *ast.File) []string {
	if astFile == nil {
		return nil
	}
	var funcs []string
	for _, decl := range astFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		// Only exported functions (uppercase first letter).
		name := fn.Name.Name
		if name == "" || !isExportedName(name) {
			continue
		}
		// Skip init, Test*, Benchmark*, Example* (test helpers).
		if name == "init" || strings.HasPrefix(name, "Test") ||
			strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Example") {
			continue
		}
		funcs = append(funcs, name)
	}
	return funcs
}

// diffExportedFuncs returns names in `newFuncs` not present in `oldFuncs`.
func diffExportedFuncs(newFuncs, oldFuncs []string) []string {
	oldSet := make(map[string]bool, len(oldFuncs))
	for _, f := range oldFuncs {
		oldSet[f] = true
	}
	var added []string
	for _, f := range newFuncs {
		if !oldSet[f] {
			added = append(added, f)
		}
	}
	return added
}

// countSubstantiveGoLines counts non-blank, non-comment lines in Go source.
func countSubstantiveGoLines(src string) int {
	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		// Fallback: count non-blank lines.
		count := 0
		for _, line := range strings.Split(src, "\n") {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}
		return count
	}

	// Build set of comment line ranges to exclude.
	commentLines := make(map[int]bool)
	for _, cg := range astFile.Comments {
		start := fset.Position(cg.Pos()).Line
		end := fset.Position(cg.End()).Line
		for i := start; i <= end; i++ {
			commentLines[i] = true
		}
	}

	totalLines := strings.Count(src, "\n") + 1
	count := 0
	for i := 1; i <= totalLines; i++ {
		if commentLines[i] {
			continue
		}
		// Get the line content.
		lines := strings.Split(src, "\n")
		if i <= len(lines) && strings.TrimSpace(lines[i-1]) != "" {
			count++
		}
	}
	return count
}

// isExportedName returns true for Go-exported identifiers (uppercase first letter).
func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

// isGeneratedOrExcludedPath returns true for paths that should not trigger
// the missing test companion check.
func isGeneratedOrExcludedPath(path string) bool {
	// Check for //go:generate markers is too expensive (would require parsing).
	// Instead, use path-based heuristics.
	lower := strings.ToLower(path)
	excluded := []string{
		string(filepath.Separator) + "vendor" + string(filepath.Separator),
		string(filepath.Separator) + "third_party" + string(filepath.Separator),
		string(filepath.Separator) + "testdata" + string(filepath.Separator),
		string(filepath.Separator) + "mocks" + string(filepath.Separator),
		string(filepath.Separator) + ".ggcode" + string(filepath.Separator),
	}
	for _, ex := range excluded {
		if strings.Contains(lower, ex) {
			return true
		}
	}

	// Check for common generated file patterns.
	base := filepath.Base(path)
	generatedPrefixes := []string{"zz_", "gen_", "generated_", "auto_"}
	for _, prefix := range generatedPrefixes {
		if strings.HasPrefix(strings.ToLower(base), prefix) {
			return true
		}
	}

	// Check for //go:generate or generated markers in file.
	// This is handled via a quick content check by the caller.
	return false
}

// isGeneratedFileContent does a quick check for generated file markers.
// This is called separately to avoid parsing overhead for most files.
func isGeneratedFileContent(content string) bool {
	// Check first 500 chars for common generated markers.
	preview := content
	if len(preview) > 500 {
		preview = preview[:500]
	}
	markers := []string{
		"// Code generated",
		"// DO NOT EDIT",
		"// Auto generated",
		"//nolint:goimports",
	}
	lowerPreview := strings.ToLower(preview)
	for _, marker := range markers {
		if strings.Contains(lowerPreview, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// CheckMissingTestCompanionWithFS is the filesystem-aware entry point.
// It wraps checkMissingTestCompanion with a generated-file content check.
// This is called from agent_tool.go after a successful write.
func CheckMissingTestCompanionWithFS(filePath, oldContent, newContent string) string {
	if isGeneratedFileContent(newContent) {
		return ""
	}
	// Quick check: if the file doesn't exist on disk yet (write failed), skip.
	if _, err := os.Stat(filePath); err != nil {
		return ""
	}
	return checkMissingTestCompanion(filePath, oldContent, newContent)
}
