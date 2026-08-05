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
// stringCheck wraps a func(string, string, string) string into the
// []string-returning check signature, returning nil for empty results.
func stringCheck(fn func(string, string, string) string) func(CheckContext) []string {
	return func(ctx CheckContext) []string {
		if w := fn(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
			return []string{w}
		}
		return nil
	}
}

// sliceCheck wraps a func(string, string, string) []string directly.
func sliceCheck(fn func(string, string, string) []string) func(CheckContext) []string {
	return func(ctx CheckContext) []string {
		return fn(ctx.FilePath, ctx.OldContent, ctx.NewContent)
	}
}

// stringCheckNew wraps a func(string, string) string (no oldContent).
func stringCheckNew(fn func(string, string) string) func(CheckContext) []string {
	return func(ctx CheckContext) []string {
		if w := fn(ctx.FilePath, ctx.NewContent); w != "" {
			return []string{w}
		}
		return nil
	}
}

func registerAllChecks() {
	allChecks = []IntegrityCheck{
		// --- Any-language checks ---
		{Name: "binary-corruption", Run: func(ctx CheckContext) []string {
			count := strings.Count(ctx.NewContent, "\x00")
			if count == 0 {
				return nil
			}
			return []string{fmt.Sprintf("File contains %d null byte(s) (\\x00) - content may be corrupted or incorrectly encoded. Check encoding and re-write if needed.", count)}
		}},
		{Name: "content-loss", Run: func(ctx CheckContext) []string {
			if strings.TrimSpace(ctx.OldContent) == "" || strings.TrimSpace(ctx.NewContent) != "" {
				return nil
			}
			return []string{fmt.Sprintf("This edit resulted in an EMPTY file (was %d bytes before). Verify this was intended - the old_text match may have consumed the entire file content.", len(ctx.OldContent))}
		}},
		{Name: "debug-statements", Run: sliceCheck(checkDebugStatements)},
		{Name: "merge-conflict-markers", Run: stringCheckNew(checkMergeConflictMarkers)},
		{Name: "content-growth", Run: stringCheck(checkContentGrowth)},
		{Name: "edit-blast-radius", Run: stringCheck(checkEditBlastRadius)},
		{Name: "placeholder-code", Run: sliceCheck(checkPlaceholderCode)},
		{Name: "commented-code", Run: sliceCheck(checkCommentedCodeBlocks)},
		{Name: "delimiter-balance", Run: stringCheckNew(checkDelimiterBalance)},
		{Name: "unicode-chars", Run: stringCheck(checkUnicodeChars)},
		{Name: "trailing-whitespace", Run: stringCheck(checkTrailingWhitespace)},
		{Name: "hardcoded-paths", Run: sliceCheck(checkHardcodedPaths)},
		{Name: "logging-intel", Langs: []Language{LangGo, LangJSTS}, Run: sliceCheck(checkLoggingIntel)},

		// --- Markup / JS-TS checks ---
		{Name: "tag-balance", Langs: []Language{LangMarkup, LangJSTS}, Run: stringCheckNew(checkTagBalance)},
		{Name: "jsts-antipatterns", Langs: []Language{LangJSTS}, Run: stringCheck(checkJSTSAntiPatterns)},
		{Name: "accessibility", Langs: []Language{LangMarkup, LangJSTS}, Run: sliceCheck(checkAccessibility)},
		{Name: "i18n", Langs: []Language{LangJSTS, LangGo}, Run: sliceCheck(checkI18n)},

		// --- Config checks ---
		{Name: "config-syntax", Langs: []Language{LangConfig}, Run: stringCheckNew(configSyntaxCheck)},

		// --- Python checks ---
		{Name: "python-indentation", Langs: []Language{LangPython}, Run: stringCheckNew(checkPythonIndentation)},

		// --- Go checks ---
		{Name: "go-syntax", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			if ctx.GoAST != nil || strings.TrimSpace(ctx.NewContent) == "" {
				return nil
			}
			fset := token.NewFileSet()
			_, err := parser.ParseFile(fset, ctx.FilePath, ctx.NewContent, 0)
			return goSyntaxWarnings(ctx.FilePath, err)
		}},
		{Name: "go-imports", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			if ctx.GoAST == nil {
				return nil
			}
			return checkGoImportsASTWithDir(ctx.FilePath, ctx.GoAST, filepath.Dir(ctx.FilePath))
		}},
		{Name: "duplicate-decls", Langs: []Language{LangGo}, Run: stringCheck(checkDuplicateDeclarations)},
		{Name: "context-leak", Langs: []Language{LangGo}, Run: stringCheck(checkContextLeak)},
		{Name: "test-gaming", Langs: []Language{LangGo}, Run: stringCheck(checkTestGaming)},
		{Name: "resource-leak", Langs: []Language{LangGo}, Run: sliceCheck(func(fp, _, nc string) []string {
			return checkResourceLeaks(fp, nc)
		})},
		{Name: "error-swallowing", Langs: []Language{LangGo}, Run: sliceCheck(checkErrorSwallowing)},
		{Name: "defer-in-loop", Langs: []Language{LangGo}, Run: sliceCheck(checkDeferInLoop)},
		{Name: "unchecked-type-assert", Langs: []Language{LangGo}, Run: sliceCheck(checkUncheckedTypeAssert)},
		{Name: "select-timer-leak", Langs: []Language{LangGo}, Run: sliceCheck(checkSelectTimerLeak)},
		{Name: "lost-cancel", Langs: []Language{LangGo}, Run: sliceCheck(checkLostCancel)},
		{Name: "http-timeout", Langs: []Language{LangGo}, Run: sliceCheck(checkHTTPTimeout)},
		{Name: "premature-exit", Langs: []Language{LangGo}, Run: sliceCheck(checkPrematureExit)},
		{Name: "lock-without-unlock", Langs: []Language{LangGo}, Run: sliceCheck(checkLockWithoutUnlock)},
		{Name: "unbounded-recursion", Langs: []Language{LangGo}, Run: sliceCheck(checkUnboundedRecursion)},
		{Name: "error-order", Langs: []Language{LangGo}, Run: sliceCheck(checkErrorOrder)},
		{Name: "printf-format", Langs: []Language{LangGo}, Run: sliceCheck(checkPrintfFormat)},
		{Name: "receiver-consistency", Langs: []Language{LangGo}, Run: sliceCheck(checkReceiverConsistency)},
		{Name: "variable-shadowing", Langs: []Language{LangGo}, Run: sliceCheck(checkVarShadowing)},
		{Name: "ignored-error-return", Langs: []Language{LangGo}, Run: sliceCheck(checkIgnoredErrorReturn)},
		{Name: "range-copy-mod", Langs: []Language{LangGo}, Run: sliceCheck(checkRangeCopyMod)},
		{Name: "goroutine-leak", Langs: []Language{LangGo}, Run: sliceCheck(checkGoroutineLeak)},
		{Name: "waitgroup-misuse", Langs: []Language{LangGo}, Run: sliceCheck(checkWaitGroupMisuse)},
		{Name: "race-verify-hint", Langs: []Language{LangGo}, Run: sliceCheck(checkRaceVerifyHint)},
		{Name: "error-wrapping", Langs: []Language{LangGo}, Run: sliceCheck(checkErrorWrapping)},
		{Name: "error-msg-quality", Langs: []Language{LangGo}, Run: sliceCheck(checkErrorMsgQuality)},
		{Name: "interface-compliance", Langs: []Language{LangGo}, Run: stringCheck(checkInterfaceCompliance)},
		{Name: "interface-design", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			if ctx.GoAST == nil {
				return nil
			}
			return checkInterfaceDesign(ctx)
		}},
		{Name: "nil-map-write", Langs: []Language{LangGo}, Run: stringCheck(checkNilMapWrite)},
		{Name: "loop-perf", Langs: []Language{LangGo}, Run: sliceCheck(checkLoopPerf)},
		{Name: "string-efficiency", Langs: []Language{LangGo}, Run: sliceCheck(checkStringEfficiency)},
		{Name: "unreachable-code", Langs: []Language{LangGo}, Run: sliceCheck(checkUnreachableCode)},
		{Name: "dead-code", Langs: []Language{LangGo}, Run: sliceCheck(checkDeadCode)},
		{Name: "hollow-test", Langs: []Language{LangGo}, Run: stringCheck(checkAssertionPresence)},
		{Name: "test-isolation", Langs: []Language{LangGo}, Run: stringCheck(checkTestIsolation)},
		{Name: "deprecated-api", Langs: []Language{LangGo}, Run: stringCheck(checkDeprecatedAPI)},
		{Name: "numeric-conversion", Langs: []Language{LangGo}, Run: stringCheck(checkUnsafeNumericConversion)},
		{Name: "duplicate-code", Langs: []Language{LangGo}, Run: sliceCheck(checkDuplicateCode)},
		{Name: "magic-number", Langs: []Language{LangGo}, Run: stringCheck(checkMagicNumbers)},
		{Name: "panic-safety", Langs: []Language{LangGo}, Run: sliceCheck(checkPanicSafety)},
		{Name: "nil-deref-after-error", Langs: []Language{LangGo}, Run: stringCheck(checkNilDerefAfterError)},
		{Name: "breaking-change", Langs: []Language{LangGo}, Run: stringCheck(checkBreakingChanges)},
		{Name: "self-assignment", Langs: []Language{LangGo}, Run: stringCheck(checkSelfAssignment)},
		{Name: "suspicious-comparison", Langs: []Language{LangGo}, Run: stringCheck(checkSuspiciousComparison)},
		{Name: "exit-path", Langs: []Language{LangGo}, Run: sliceCheck(checkExitPath)},
		{Name: "nplus1-loop", Langs: []Language{LangGo}, Run: sliceCheck(checkNPlus1Loop)},
		{Name: "missing-prealloc", Langs: []Language{LangGo}, Run: sliceCheck(checkMissingPrealloc)},
		{Name: "map-prealloc", Langs: []Language{LangGo}, Run: sliceCheck(checkMapPrealloc)},
		{Name: "concurrent-map-access", Langs: []Language{LangGo}, Run: stringCheck(checkConcurrentMapAccess)},
		{Name: "struct-tag-consistency", Langs: []Language{LangGo}, Run: sliceCheck(checkStructTagConsistency)},
		{Name: "copylock", Langs: []Language{LangGo}, Run: sliceCheck(checkCopylock)},
		{Name: "excessive-params", Langs: []Language{LangGo}, Run: sliceCheck(checkExcessiveParams)},
		{Name: "excessive-returns", Langs: []Language{LangGo}, Run: sliceCheck(checkExcessiveReturns)},
		{Name: "exported-doc", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			if ctx.GoAST == nil {
				return nil
			}
			return checkMissingExportedDocsAST(ctx.FilePath, ctx.OldContent, ctx.GoAST)
		}},

		// --- Multi-language checks ---
		{Name: "insecure-patterns", Langs: []Language{LangGo, LangJSTS, LangPython}, Run: sliceCheck(checkInsecurePatterns)},
		// --- Dependency manifest checks ---
		{Name: "dep-breaking-change", Run: stringCheck(checkBreakingChangeDepAsString)},
		{Name: "flaky-test-patterns", Langs: []Language{LangGo, LangJSTS, LangPython}, Run: stringCheck(checkFlakyTestPatterns)},

		// --- Dependency vulnerability check (SCA) ---
		// Triggers on dependency manifest files: go.mod, package.json, etc.
		{Name: "dependency-vulns", Run: stringCheck(checkDependencyVulnsAsString)},
		// --- Supply chain: typosquatting detection ---
		// Flags new dependencies whose names are 1-2 edits from popular packages.
		{Name: "typosquatting", Run: stringCheck(checkTyposquattingAsString)},
		// --- HTTP plaintext detection ---
		// Flags http:// URLs in source code that should use https://.
		{Name: "http-plaintext", Langs: []Language{LangGo, LangJSTS, LangPython}, Run: sliceCheck(checkHTTPPlaintext)},
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
