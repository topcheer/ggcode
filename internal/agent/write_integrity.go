package agent

import (
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Post-write file integrity validation.
//
// This module now uses a Check Registry (see check_registry.go) that runs
// checks in parallel with language-aware filtering and panic recovery.
// Each check is registered as a self-contained unit with language metadata,
// enabling:
//   - Go-only checks to be skipped for non-Go files (saves CPU)
//   - Parallel execution across all applicable checks
//   - Fault isolation: a panic in one check never crashes the pipeline
//   - External command safety: checks must handle missing tools gracefully

const (
	maxIntegrityWarnings = 3
	maxGoSyntaxErrors    = 2
)

// checkWriteIntegrity validates the content of a file after a write/edit.
// Delegates to the Check Registry for parallel, language-aware execution.
func checkWriteIntegrity(filePath, oldContent, newContent string) string {
	ctx := newCheckContext(filePath, oldContent, newContent)
	warnings := runChecksParallel(ctx)
	return formatWarnings(warnings)
}

// registerAllChecks registers all post-write integrity checks with their
// language filters. Called from check_registry.go init().
//
// Language filters ensure Go-only checks are never executed for non-Go files,
// eliminating wasted CPU and avoiding false positives.
func registerAllChecks() {
	allChecks = []IntegrityCheck{
		// 1. Binary corruption (any language)
		{
			Name: "binary-corruption",
			Run: func(ctx CheckContext) []string {
				count := strings.Count(ctx.NewContent, "\x00")
				if count == 0 {
					return nil
				}
				return []string{fmt.Sprintf("File contains %d null byte(s) (\\x00) - content may be corrupted or incorrectly encoded. Check encoding and re-write if needed.", count)}
			},
		},
		// 2. Content loss - file became empty (any language)
		{
			Name: "content-loss",
			Run: func(ctx CheckContext) []string {
				if strings.TrimSpace(ctx.OldContent) == "" || strings.TrimSpace(ctx.NewContent) != "" {
					return nil
				}
				return []string{fmt.Sprintf("This edit resulted in an EMPTY file (was %d bytes before). Verify this was intended - the old_text match may have consumed the entire file content.", len(ctx.OldContent))}
			},
		},
		// 3. Go syntax errors (Go only)
		{
			Name:  "go-syntax",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if ctx.GoAST != nil {
					return nil // parsed OK
				}
				if strings.TrimSpace(ctx.NewContent) == "" {
					return nil
				}
				// Re-parse to get the error details.
				fset := token.NewFileSet()
				_, err := parser.ParseFile(fset, ctx.FilePath, ctx.NewContent, 0)
				return goSyntaxWarnings(ctx.FilePath, err)
			},
		},
		// 4. Debug statement detection (any language)
		{
			Name: "debug-statements",
			Run: func(ctx CheckContext) []string {
				return checkDebugStatements(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 5. Go import analysis (Go only)
		{
			Name:  "go-imports",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if ctx.GoAST == nil {
					return nil
				}
				fileDir := filepath.Dir(ctx.FilePath)
				return checkGoImportsASTWithDir(ctx.FilePath, ctx.GoAST, fileDir)
			},
		},
		// 6. Merge conflict markers (any language)
		{
			Name: "merge-conflict-markers",
			Run: func(ctx CheckContext) []string {
				if w := checkMergeConflictMarkers(ctx.FilePath, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 7. Content growth (any language)
		{
			Name: "content-growth",
			Run: func(ctx CheckContext) []string {
				if w := checkContentGrowth(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 7b. Edit blast radius (any language)
		{
			Name: "edit-blast-radius",
			Run: func(ctx CheckContext) []string {
				if w := checkEditBlastRadius(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 8. Placeholder code (any language)
		{
			Name: "placeholder-code",
			Run: func(ctx CheckContext) []string {
				return checkPlaceholderCode(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 8b. Commented-out code (any language)
		{
			Name: "commented-code",
			Run: func(ctx CheckContext) []string {
				return checkCommentedCodeBlocks(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 9. Duplicate declarations (Go)
		{
			Name:  "duplicate-decls",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkDuplicateDeclarations(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 10. Delimiter balance (non-Go, self-filters internally)
		{
			Name: "delimiter-balance",
			Run: func(ctx CheckContext) []string {
				if w := checkDelimiterBalance(ctx.FilePath, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 10b. HTML/XML/JSX tag balance (markup)
		{
			Name:  "tag-balance",
			Langs: []Language{LangMarkup, LangJSTS},
			Run: func(ctx CheckContext) []string {
				if w := checkTagBalance(ctx.FilePath, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 11. Config file syntax (config files)
		{
			Name:  "config-syntax",
			Langs: []Language{LangConfig},
			Run: func(ctx CheckContext) []string {
				if w := configSyntaxCheck(ctx.FilePath, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 11b. Python indentation (Python only)
		{
			Name:  "python-indentation",
			Langs: []Language{LangPython},
			Run: func(ctx CheckContext) []string {
				if w := checkPythonIndentation(ctx.FilePath, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 12. Unicode characters (any language)
		{
			Name: "unicode-chars",
			Run: func(ctx CheckContext) []string {
				if w := checkUnicodeChars(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 13. Context leak (Go only)
		{
			Name:  "context-leak",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkContextLeak(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 14. Test gaming (Go only)
		{
			Name:  "test-gaming",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkTestGaming(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 15. Trailing whitespace (non-Go, self-filters internally)
		{
			Name: "trailing-whitespace",
			Run: func(ctx CheckContext) []string {
				if w := checkTrailingWhitespace(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 16. Hardcoded paths (any language)
		{
			Name: "hardcoded-paths",
			Run: func(ctx CheckContext) []string {
				return checkHardcodedPaths(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 17. Resource leak (Go only)
		{
			Name:  "resource-leak",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkResourceLeaks(ctx.FilePath, ctx.NewContent)
			},
		},
		// 18. Error swallowing (Go only)
		{
			Name:  "error-swallowing",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkErrorSwallowing(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 19. Defer in loop (Go only)
		{
			Name:  "defer-in-loop",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkDeferInLoop(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 20. Unchecked type assertion (Go only)
		{
			Name:  "unchecked-type-assert",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkUncheckedTypeAssert(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 21. Select timer leak (Go only)
		{
			Name:  "select-timer-leak",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkSelectTimerLeak(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 22. HTTP timeout (Go only)
		{
			Name:  "http-timeout",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkHTTPTimeout(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 23. Premature exit (Go only)
		{
			Name:  "premature-exit",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkPrematureExit(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 24. Lock without unlock (Go only)
		{
			Name:  "lock-without-unlock",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkLockWithoutUnlock(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 25. Unbounded recursion (Go only)
		{
			Name:  "unbounded-recursion",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkUnboundedRecursion(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 26. Error order (Go only)
		{
			Name:  "error-order",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkErrorOrder(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 27. Printf format (Go only)
		{
			Name:  "printf-format",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkPrintfFormat(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 28. Receiver consistency (Go only)
		{
			Name:  "receiver-consistency",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkReceiverConsistency(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 29. Variable shadowing (Go only)
		{
			Name:  "variable-shadowing",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkVarShadowing(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 30. Ignored error return (Go only)
		{
			Name:  "ignored-error-return",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkIgnoredErrorReturn(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 31. Range copy modification (Go only)
		{
			Name:  "range-copy-mod",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkRangeCopyMod(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 32. Goroutine leak (Go only)
		{
			Name:  "goroutine-leak",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkGoroutineLeak(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 33. Error wrapping (Go only)
		{
			Name:  "error-wrapping",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkErrorWrapping(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 33b. Interface compliance (Go only)
		{
			Name:  "interface-compliance",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkInterfaceCompliance(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 34b. Nil map write (Go only)
		{
			Name:  "nil-map-write",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkNilMapWrite(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 35. Insecure patterns (multi-language: Go, JS/TS, Python)
		{
			Name:  "insecure-patterns",
			Langs: []Language{LangGo, LangJSTS, LangPython},
			Run: func(ctx CheckContext) []string {
				return checkInsecurePatterns(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 36. Loop performance (Go only)
		{
			Name:  "loop-perf",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkLoopPerf(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 37. Unreachable code (Go only)
		{
			Name:  "unreachable-code",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkUnreachableCode(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 38. Hollow test / assertion presence (Go only)
		{
			Name:  "hollow-test",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkAssertionPresence(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 39. Deprecated API (Go only)
		{
			Name:  "deprecated-api",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkDeprecatedAPI(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 40. Unsafe numeric conversion (Go only)
		{
			Name:  "numeric-conversion",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkUnsafeNumericConversion(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 41. Flaky test patterns (multi-language)
		{
			Name:  "flaky-test-patterns",
			Langs: []Language{LangGo, LangJSTS, LangPython},
			Run: func(ctx CheckContext) []string {
				if w := checkFlakyTestPatterns(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 42. Duplicate code (Go only)
		{
			Name:  "duplicate-code",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkDuplicateCode(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 43. Magic number (Go only)
		{
			Name:  "magic-number",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkMagicNumbers(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 45. Panic safety (Go only)
		{
			Name:  "panic-safety",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkPanicSafety(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 46. Breaking change (Go only)
		{
			Name:  "breaking-change",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkBreakingChanges(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 47. Self-assignment (Go only)
		{
			Name:  "self-assignment",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkSelfAssignment(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
		// 48. N+1 I/O in loop (Go only)
		{
			Name:  "nplus1-loop",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				return checkNPlus1Loop(ctx.FilePath, ctx.OldContent, ctx.NewContent)
			},
		},
		// 49. Concurrent map access (Go only)
		{
			Name:  "concurrent-map-access",
			Langs: []Language{LangGo},
			Run: func(ctx CheckContext) []string {
				if w := checkConcurrentMapAccess(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
					return []string{w}
				}
				return nil
			},
		},
	}

	debug.Log("integrity", "registered %d post-write checks", len(allChecks))
}

// goSyntaxWarnings converts a parser error into human-readable syntax error
// descriptions.
func goSyntaxWarnings(filename string, parseErr error) []string {
	if parseErr == nil {
		return nil
	}

	var warnings []string

	if el, ok := parseErr.(scanner.ErrorList); ok {
		for i, e := range el {
			if i >= maxGoSyntaxErrors {
				remaining := len(el) - maxGoSyntaxErrors
				warnings = append(warnings,
					fmt.Sprintf("...and %d more syntax error(s) in %s", remaining, filename))
				break
			}
			warnings = append(warnings, e.Error())
		}
		return warnings
	}

	warnings = append(warnings, parseErr.Error())
	return warnings
}

// checkGoSyntax is a convenience wrapper for tests.
func checkGoSyntax(filename, src string) []string {
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, filename, src, 0)
	return goSyntaxWarnings(filename, err)
}

// checkEditBlastRadius detects when a single edit modifies a high proportion
// of a file's lines, signaling an unintended full rewrite.
func checkEditBlastRadius(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(oldContent) == "" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	oldLines := strings.Count(strings.TrimRight(oldContent, "\n"), "\n") + 1
	if oldLines < 20 {
		return ""
	}

	oldSet := make(map[string]int)
	for _, line := range strings.Split(oldContent, "\n") {
		oldSet[line]++
	}
	newSet := make(map[string]int)
	for _, line := range strings.Split(newContent, "\n") {
		newSet[line]++
	}

	removed := 0
	for line, oldCount := range oldSet {
		newCount := newSet[line]
		if oldCount > newCount {
			removed += oldCount - newCount
		}
	}

	added := 0
	for line, newCount := range newSet {
		oldCount := oldSet[line]
		if newCount > oldCount {
			added += newCount - oldCount
		}
	}

	changed := added + removed
	if changed == 0 {
		return ""
	}

	ratio := float64(changed) / float64(oldLines)
	if ratio >= 0.60 {
		newLines := strings.Count(strings.TrimRight(newContent, "\n"), "\n") + 1
		return fmt.Sprintf(
			"This edit modified %d of %d lines (%.0f%% of the file): +%d added, -%d removed -> %d lines. "+
				"This is a high blast-radius change - verify this was intentional and not an overly broad old_text match or accidental rewrite. "+
				"For targeted changes, prefer edit_file with a more specific old_text anchor.",
			changed, oldLines, ratio*100, added, removed, newLines)
	}

	return ""
}
