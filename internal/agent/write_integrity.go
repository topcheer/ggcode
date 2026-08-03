package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Post-write file integrity validation.
//
// Research basis: Claude Code uses LSP for immediate post-edit syntax feedback;
// Cursor runs in-process diagnostics; OpenHands/Cline rely on post-edit build
// verification. Aider shows a live diff and validates structure before commit.
//
// ggcode already has LSP diagnostics integration and verify hints, but LSP
// requires a running language server (not always available, e.g. for generated
// code or exotic languages) and verify hints only suggest running the build
// command - they don't catch the error inline.
//
// This module provides a lightweight, always-available structural validation
// that runs synchronously after successful file writes and catches the most
// common post-edit issues with zero external dependencies:
//
//  1. Go syntax errors - uses go/parser from the standard library to catch
//     syntax issues immediately (<1ms for typical files). This is the most
//     impactful check since this is a Go project and syntax errors are the
//     #1 cause of failed builds after agent edits.
//  2. Binary corruption - null bytes in what should be a text file indicate
//     encoding issues or accidental binary writes.
//  3. Content loss - a non-empty file becoming empty/whitespace-only after an
//     edit signals a catastrophic edit failure (e.g., old_text consumed the
//     entire file).
//
// When issues are found, a concise warning is injected into the tool result so
// the agent can fix the problem in the same turn, avoiding a wasted build/test
// cycle iteration. The check is non-blocking and cannot hang (go/parser is a
// pure in-memory operation).

const (
	// maxIntegrityWarnings caps the number of warnings per write to avoid
	// flooding the tool result with excessive output.
	maxIntegrityWarnings = 3

	// maxGoSyntaxErrors limits how many Go syntax errors we report. Go files
	// with many errors produce a cascade; the first 2 are usually the root cause.
	maxGoSyntaxErrors = 2
)

// checkWriteIntegrity validates the content of a file after a write/edit.
// Returns a non-empty guidance string if issues are detected.
//
// Parameters:
//   - filePath: the path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkWriteIntegrity(filePath, oldContent, newContent string) string {
	var warnings []string

	// 1. Binary corruption: null bytes in what should be a text file.
	if strings.ContainsRune(newContent, 0) {
		count := strings.Count(newContent, "\x00")
		warnings = append(warnings,
			fmt.Sprintf("File contains %d null byte(s) (\\x00) - content may be corrupted or incorrectly encoded. Check encoding and re-write if needed.", count))
	}

	// 2. Content loss: non-empty source file became empty/whitespace-only.
	//    This catches the common failure where edit_file's old_text matches
	//    and removes the entire file content.
	if strings.TrimSpace(oldContent) != "" && strings.TrimSpace(newContent) == "" {
		warnings = append(warnings,
			fmt.Sprintf("This edit resulted in an EMPTY file (was %d bytes before). "+
				"Verify this was intended - the old_text match may have consumed the entire file content.", len(oldContent)))
	}

	// 3+5. Parse Go AST ONCE and share across syntax + import checks.
	// This avoids calling parser.ParseFile 2-3 times per .go file write.
	var goAST *ast.File
	var goSyntaxErr error
	if filepath.Ext(filePath) == ".go" && strings.TrimSpace(newContent) != "" {
		fset := token.NewFileSet()
		goAST, goSyntaxErr = parser.ParseFile(fset, filePath, newContent, 0)

		// Syntax errors.
		if syntaxWarnings := goSyntaxWarnings(filePath, goSyntaxErr); len(syntaxWarnings) > 0 {
			warnings = append(warnings, syntaxWarnings...)
		}

		// Import analysis only if AST parsed cleanly.
		// Use the file's directory as the working dir so go.mod-aware
		// third-party import detection can find the module file.
		if goSyntaxErr == nil {
			fileDir := filepath.Dir(filePath)
			if importWarnings := checkGoImportsASTWithDir(filePath, goAST, fileDir); len(importWarnings) > 0 {
				warnings = append(warnings, importWarnings...)
			}
		}
	}

	// 4. Debug statement detection - flags leftover debug prints/logs that
	//    agents commonly introduce (console.log, debugger, dd(), etc.).
	if debugWarnings := checkDebugStatements(filePath, oldContent, newContent); len(debugWarnings) > 0 {
		warnings = append(warnings, debugWarnings...)
	}

	// 6. Merge conflict markers - always a build failure. Agents sometimes
	//    copy conflict markers from context verbatim into written files.
	if markerWarn := checkMergeConflictMarkers(filePath, newContent); markerWarn != "" {
		warnings = append(warnings, markerWarn)
	}

	// 7. Content duplication / massive growth - catches accidental double-paste
	//    or whole-file duplication (file growing 5x+ in one edit).
	if growthWarn := checkContentGrowth(filePath, oldContent, newContent); growthWarn != "" {
		warnings = append(warnings, growthWarn)
	}

	// 7b. Edit blast radius - detects when a single edit modifies a high
	//     percentage of lines (>=60%), which often indicates an unintended
	//     full rewrite rather than a targeted change. This catches the
	//     dangerous failure mode where edit_file's old_text matches too
	//     broadly (e.g. matching a common pattern) or write_file replaces
	//     a large file's entire content with something very different.
	//     Unlike checkContentGrowth (which only fires at 5x+ growth), this
	//     catches large SHRINKS and high-churn rewrites where the line count
	//     stays similar but most content changes.
	if blastWarn := checkEditBlastRadius(filePath, oldContent, newContent); blastWarn != "" {
		warnings = append(warnings, blastWarn)
	}

	// 8. Placeholder / stub code - detects incomplete implementations that
	//    agents commonly leave behind (panic("not implemented"), vague TODOs).
	//    Only flags NEW placeholders introduced by this edit.
	if placeholderWarnings := checkPlaceholderCode(filePath, oldContent, newContent); len(placeholderWarnings) > 0 {
		warnings = append(warnings, placeholderWarnings...)
	}

	// 8b. Commented-out code blocks - detects blocks of commented-out executable
	//     code introduced by this edit. Agents frequently comment out old code
	//     instead of deleting it, leaving dead code that clutters diffs.
	if commentedWarnings := checkCommentedCodeBlocks(filePath, oldContent, newContent); len(commentedWarnings) > 0 {
		warnings = append(warnings, commentedWarnings...)
	}

	// 9. Duplicate declaration detection - detects duplicate functions, types,
	//    imports, consts, and vars introduced by this edit. These are guaranteed
	//    compilation errors that waste iterations if not caught immediately.
	if dupWarn := checkDuplicateDeclarations(filePath, oldContent, newContent); dupWarn != "" {
		warnings = append(warnings, dupWarn)
	}

	// 10. Delimiter balance - validates (), {}, [] are balanced for non-Go source
	//     files (JS, TS, Python, Rust, Java, Dart, JSON, YAML, CSS). Go files are
	//    covered by go/parser above. This catches the common edit failure where
	//    the agent adds or removes a bracket without its match.
	if delimWarn := checkDelimiterBalance(filePath, newContent); delimWarn != "" {
		warnings = append(warnings, delimWarn)
	}

	// 10b. HTML/XML/JSX tag balance - validates markup tags are properly
	//      balanced in HTML, JSX, TSX, Vue, Svelte, and XML files. Catches a
	//      common agent failure that bracket checking cannot detect.
	if tagWarn := checkTagBalance(filePath, newContent); tagWarn != "" {
		warnings = append(warnings, tagWarn)
	}

	// 11. Config file syntax validation - parses JSON, YAML, TOML, XML after
	//     write to catch malformed config files that would cause runtime failures.
	//     Uses existing project parsers (zero new dependencies).
	if configWarn := configSyntaxCheck(filePath, newContent); configWarn != "" {
		warnings = append(warnings, configWarn)
	}

	// 11b. Python indentation consistency - detects mixed tabs/spaces in
	//      indentation runs, which cause TabError in Python 3. This is
	//      syntactically significant (unlike Go where gofmt handles it).
	if pyIndentWarn := checkPythonIndentation(filePath, newContent); pyIndentWarn != "" {
		warnings = append(warnings, pyIndentWarn)
	}

	// 12. Problematic Unicode character detection - catches smart quotes,
	//     non-breaking spaces, zero-width characters, and other invisible
	//     Unicode that LLMs frequently introduce. Delta-based detection
	//     only flags characters introduced by this edit.
	if unicodeWarn := checkUnicodeChars(filePath, oldContent, newContent); unicodeWarn != "" {
		warnings = append(warnings, unicodeWarn)
	}

	// 13. Context propagation leak detection - flags context.TODO() or
	//     context.Background() used in functions that receive a ctx parameter,
	//     which breaks cancellation/deadline/trace propagation. Delta-aware.
	if ctxLeakWarn := checkContextLeak(filePath, oldContent, newContent); ctxLeakWarn != "" {
		warnings = append(warnings, ctxLeakWarn)
	}

	// 14. Test gaming detection - flags suspicious modifications to test files
	//     that weaken verification: deleted test functions, added skip directives,
	//     removed assertions. Delta-based (only flags changes introduced by this edit).
	if testGamingWarn := checkTestGaming(filePath, oldContent, newContent); testGamingWarn != "" {
		warnings = append(warnings, testGamingWarn)
	}

	// 15. Trailing whitespace detection - flags trailing spaces/tabs newly
	//     introduced by this edit. Causes lint failures, git diff noise, and
	//     pre-commit hook rejections. Delta-based; Go files skipped (gofmt handles).
	if twWarn := checkTrailingWhitespace(filePath, oldContent, newContent); twWarn != "" {
		warnings = append(warnings, twWarn)
	}

	// 16. Hardcoded absolute path detection - flags machine-specific paths
	//     (/Users/.../, /home/.../, /root/.../, C:\Users\..\) introduced by
	//     this edit. These break portability, CI/CD, and collaboration.
	if pathWarnings := checkHardcodedPaths(filePath, oldContent, newContent); len(pathWarnings) > 0 {
		warnings = append(warnings, pathWarnings...)
	}

	// 17. Resource leak detection - AST-based analysis of Go functions to find
	//     resource acquisitions (os.Open, http.Get, net.Listen) without matching
	//     defer Close() cleanup. LLMs frequently omit cleanup calls.
	if leakWarnings := checkResourceLeaks(filePath, newContent); len(leakWarnings) > 0 {
		warnings = append(warnings, leakWarnings...)
	}

	// 18. Error swallowing detection - AST-based analysis to catch empty error
	//     handlers (if err != nil {}) and bare returns that drop errors
	//     (if err != nil { return } in error-returning functions). Delta-aware.
	if swallowWarnings := checkErrorSwallowing(filePath, oldContent, newContent); len(swallowWarnings) > 0 {
		warnings = append(warnings, swallowWarnings...)
	}

	// 19. Defer-in-loop detection - flags defer statements inside for/range
	//     loops, which cause resource accumulation (defer runs at function
	//     return, not iteration end). Delta-aware.
	if deferLoopWarnings := checkDeferInLoop(filePath, oldContent, newContent); len(deferLoopWarnings) > 0 {
		warnings = append(warnings, deferLoopWarnings...)
	}

	// 20. Unchecked type assertion detection - flags x.(T) without comma-ok
	//     guard, which causes runtime panics if the assertion fails. Delta-aware.
	if assertWarnings := checkUncheckedTypeAssert(filePath, oldContent, newContent); len(assertWarnings) > 0 {
		warnings = append(warnings, assertWarnings...)
	}

	// 21. Select-loop timer leak detection - flags time.After() inside a select
	//     within a for/range loop, which leaks timers each iteration (timer is
	//     not GC'd until it fires). Should use time.NewTimer + Stop/Reset. Delta-aware.
	if timerWarnings := checkSelectTimerLeak(filePath, oldContent, newContent); len(timerWarnings) > 0 {
		warnings = append(warnings, timerWarnings...)
	}

	// 22. HTTP client missing-timeout detection - AST-based analysis to catch
	//     HTTP requests without any timeout. http.Get/Post/Head/PostForm use
	//     DefaultClient (no timeout), and &http.Client{} without a Timeout field
	//     also hangs indefinitely. This is distinct from resource leaks (missing
	//     Close()) -- a request can have defer resp.Body.Close() but still hang
	//     forever without a timeout. Delta-aware.
	if timeoutWarnings := checkHTTPTimeout(filePath, oldContent, newContent); len(timeoutWarnings) > 0 {
		warnings = append(warnings, timeoutWarnings...)
	}

	// 23. Premature exit call detection - flags os.Exit/log.Fatal/log.Panic
	//     in non-main/init functions. These skip deferred cleanup, make functions
	//     untestable, and prevent error propagation. Delta-aware.
	if exitWarnings := checkPrematureExit(filePath, oldContent, newContent); len(exitWarnings) > 0 {
		warnings = append(warnings, exitWarnings...)
	}

	// 24. Mutex lock-without-unlock detection - flags Lock/RLock/TryLock calls
	//     without a matching Unlock/RUnlock in the same function. Causes
	//     permanent deadlocks. The existing resource_leak_check only detects
	//     resource-acquiring assignments (os.Open), not bare method calls like
	//     mu.Lock(). Delta-aware.
	if lockWarnings := checkLockWithoutUnlock(filePath, oldContent, newContent); len(lockWarnings) > 0 {
		warnings = append(warnings, lockWarnings...)
	}

	// 25. Unbounded recursion detection - flags recursive functions where every
	//     execution path calls itself (no base case/termination condition).
	//     Causes guaranteed stack overflow panics at runtime. Delta-aware.
	if recursionWarnings := checkUnboundedRecursion(filePath, oldContent, newContent); len(recursionWarnings) > 0 {
		warnings = append(warnings, recursionWarnings...)
	}

	// 26. Result-used-before-error-check detection - flags result variables
	//     (resp, val, etc.) used before their error is checked. The classic
	//     pattern is `defer resp.Body.Close()` before `if err != nil`, which
	//     causes nil pointer panics when the error is non-nil. Delta-aware.
	if orderWarnings := checkErrorOrder(filePath, oldContent, newContent); len(orderWarnings) > 0 {
		warnings = append(warnings, orderWarnings...)
	}

	// 27. Printf format string mismatch detection - flags non-constant format
	//     arguments (injection risk), redundant Sprintf inside Println (double
	//     formatting), and format verb/argument count mismatches. These cause
	//     garbled output, runtime panics, and go vet failures. Delta-aware.
	if printfWarnings := checkPrintfFormat(filePath, oldContent, newContent); len(printfWarnings) > 0 {
		warnings = append(warnings, printfWarnings...)
	}

	// 28. Inconsistent receiver name detection - flags Go types where methods
	//     use different receiver variable names (e.g., (s *Server) vs (srv *Server)).
	//     This violates Go style conventions, triggers staticcheck ST1016, and is
	//     a common LLM failure mode. Also flags "this"/"self" anti-pattern. Delta-aware.
	if receiverWarnings := checkReceiverConsistency(filePath, oldContent, newContent); len(receiverWarnings) > 0 {
		warnings = append(warnings, receiverWarnings...)
	}

	// 29. Variable shadowing detection - flags := declarations in inner scopes
	//     that hide outer variables of the same name. Error variable (err)
	//     shadowing is especially dangerous as it silently swallows errors.
	//     go vet does not flag this. Delta-aware.
	if shadowWarnings := checkVarShadowing(filePath, oldContent, newContent); len(shadowWarnings) > 0 {
		warnings = append(warnings, shadowWarnings...)
	}

	// 30. Ignored error return detection - flags calls to error-returning
	//     functions where the error is completely discarded (standalone call
	//     statement or explicit _ = discard). Distinct from error_swallow_check
	//     which only catches `if err != nil {}` empty handlers and bare returns.
	//     Delta-aware.
	if ignoredErrWarnings := checkIgnoredErrorReturn(filePath, oldContent, newContent); len(ignoredErrWarnings) > 0 {
		warnings = append(warnings, ignoredErrWarnings...)
	}

	// 31. Range loop value copy modification detection - flags modifications
	//     to range loop value variables (e.g., `item.Field = ...` in
	//     `for _, item := range slice`). Range values are copies of slice
	//     elements, so field modifications do NOT affect the original slice.
	//     This is a silent runtime bug that compiles cleanly. Delta-aware.
	if rangeCopyWarnings := checkRangeCopyMod(filePath, oldContent, newContent); len(rangeCopyWarnings) > 0 {
		warnings = append(warnings, rangeCopyWarnings...)
	}

	// 32. Goroutine lifecycle leak detection - flags `go func()` or `go someFn()`
	//     calls without any lifecycle management (WaitGroup, context cancellation,
	//     errgroup, or channel signaling) in the spawning function. These goroutines
	//     outlive the function scope, causing resource and memory leaks. The existing
	//     resource_leak_check only detects resource acquisitions (os.Open), not
	//     goroutine lifecycle problems. Delta-aware.
	if goroutineWarnings := checkGoroutineLeak(filePath, oldContent, newContent); len(goroutineWarnings) > 0 {
		warnings = append(warnings, goroutineWarnings...)
	}

	// 33. Inconsistent error wrapping detection - flags fmt.Errorf with %v
	//     instead of %w for error args, errors.New(err.Error()), and string
	//     concatenation in Errorf. These break errors.Is()/errors.As() chains.
	//     Delta-aware.
	if wrapWarnings := checkErrorWrapping(filePath, oldContent, newContent); len(wrapWarnings) > 0 {
		warnings = append(warnings, wrapWarnings...)
	}

	// 33b. Interface compliance detection - checks if edits to Go interfaces
	//      (adding/removing/renaming methods) break existing implementations in
	//      the same package. Delta-aware (only checks changed interfaces).
	if ifaceWarn := checkInterfaceCompliance(filePath, oldContent, newContent); ifaceWarn != "" {
		warnings = append(warnings, ifaceWarn)
	}

	// 34b. Nil map write detection - flags writes to uninitialized (nil) map
	//      variables (var m map[K]V; m["key"] = val). This causes a guaranteed
	//      runtime panic in Go. LLMs frequently declare map variables without
	//      make() initialization. Delta-aware.
	if nilMapWarn := checkNilMapWrite(filePath, oldContent, newContent); nilMapWarn != "" {
		warnings = append(warnings, nilMapWarn)
	}

	// 35. Insecure code pattern detection - flags security anti-patterns commonly
	//     introduced by LLMs: TLS bypass (InsecureSkipVerify), weak crypto
	//     (math/rand for tokens, MD5 for passwords), SQL injection (string
	//     concatenation in queries), command injection (shell+concat). Multi-language
	//     (Go, JS/TS, Python). Delta-aware.
	if insecureWarnings := checkInsecurePatterns(filePath, oldContent, newContent); len(insecureWarnings) > 0 {
		warnings = append(warnings, insecureWarnings...)
	}

	// 36. Loop performance anti-pattern detection - flags O(n^2) string
	//     building inside for/range loops (string += and fmt.Sprintf concat).
	//     LLMs frequently generate these patterns which cause quadratic
	//     allocations. Suggests strings.Builder for O(n) alternative.
	//     Delta-aware.
	if perfWarnings := checkLoopPerf(filePath, oldContent, newContent); len(perfWarnings) > 0 {
		warnings = append(warnings, perfWarnings...)
	}

	// 37. Unreachable / dead code detection - flags statements that can never
	//     execute: code after return/panic/break, or dead branches (if false).
	//     LLMs frequently leave unreachable code during refactoring. Delta-aware.
	if unreachableWarnings := checkUnreachableCode(filePath, oldContent, newContent); len(unreachableWarnings) > 0 {
		warnings = append(warnings, unreachableWarnings...)
	}

	// 38. Hollow test detection - flags Go test functions (Test*) that contain
	//     zero assertion calls (t.Error, t.Fatal, require.*, assert.*). LLMs
	//     frequently generate plausible-looking test stubs that never actually
	//     verify behavior, giving false confidence. Delta-aware.
	if hollowWarn := checkAssertionPresence(filePath, oldContent, newContent); hollowWarn != "" {
		warnings = append(warnings, hollowWarn)
	}

	// 39. Deprecated API detection - flags usage of deprecated Go standard
	//     library APIs (io/ioutil package, rand.Seed, strings.Title, os.SEEK_*)
	//     that LLMs frequently recommend based on outdated training data.
	//     Provides actionable migration guidance. Delta-aware.
	if deprecatedWarn := checkDeprecatedAPI(filePath, oldContent, newContent); deprecatedWarn != "" {
		warnings = append(warnings, deprecatedWarn)
	}

	if len(warnings) == 0 {
		return ""
	}

	// Cap warnings to avoid excessive output.
	if len(warnings) > maxIntegrityWarnings {
		warnings = warnings[:maxIntegrityWarnings]
	}

	debug.Log("integrity", "post-write check found %d issue(s) in %s", len(warnings), filePath)

	var b strings.Builder
	b.WriteString("[Post-write integrity check]\n")
	for i, w := range warnings {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(w)
	}
	return b.String()
}

// goSyntaxWarnings converts a parser error into human-readable syntax error
// descriptions. The caller must have already called parser.ParseFile and pass
// the resulting error (nil means no errors).
func goSyntaxWarnings(filename string, parseErr error) []string {
	if parseErr == nil {
		return nil
	}

	var warnings []string

	// go/parser wraps errors in scanner.ErrorList (a []*scanner.Error).
	// Each error has a position and message, e.g. "main.go:5:2: expected declaration, found 'if'".
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

	// Fallback for non-ErrorList errors.
	warnings = append(warnings, parseErr.Error())
	return warnings
}

// checkGoSyntax is a convenience wrapper that parses src then calls
// goSyntaxWarnings. Used by tests and as a standalone entry point.
func checkGoSyntax(filename, src string) []string {
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, filename, src, 0)
	return goSyntaxWarnings(filename, err)
}

// checkEditBlastRadius detects when a single edit modifies a high proportion
// of a file's lines, which typically signals an unintended full rewrite rather
// than a targeted change.
//
// Research basis: All major coding agents (Claude Code, Cursor, Cline) have
// some form of change-scope awareness, but none provide inline warnings when
// a single edit unexpectedly rewrites most of a file. This is a common failure
// mode when:
//   - edit_file's old_text matches too broadly (e.g., a common function
//     signature), causing the replacement to cascade across the file
//   - write_file is used instead of edit_file, destroying original content
//   - replace_all replaces an unexpectedly common pattern
//
// Detection approach: compute a line-level diff between old and new content.
// If the number of changed lines (added + removed) exceeds 60% of the original
// line count AND the file has at least 20 lines, emit a warning. The 20-line
// minimum avoids false positives on small files where any edit is a large
// fraction of the file. The 60% threshold is empirically calibrated: targeted
// edits rarely change more than 30% of lines, while accidental rewrites almost
// always change 70%+.
//
// This complements checkContentGrowth (which only fires at 5x growth) by also
// catching:
//   - Large SHRINKS (file goes from 100 lines to 30 - 70% changed)
//   - High-churn rewrites (file stays at 100 lines but 70 of them changed)
func checkEditBlastRadius(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(oldContent) == "" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	// Count non-empty lines for both versions.
	oldLines := strings.Count(strings.TrimRight(oldContent, "\n"), "\n") + 1

	// Skip small files - ratio is meaningless for tiny files.
	if oldLines < 20 {
		return ""
	}

	// Compute changed lines using a simple set-difference approach.
	// This is O(n) and avoids importing a diff library.
	oldSet := make(map[string]int) // line -> count
	for _, line := range strings.Split(oldContent, "\n") {
		oldSet[line]++
	}
	newSet := make(map[string]int)
	for _, line := range strings.Split(newContent, "\n") {
		newSet[line]++
	}

	// Count removed lines (lines in old but not matched in new).
	removed := 0
	for line, oldCount := range oldSet {
		newCount := newSet[line]
		if oldCount > newCount {
			removed += oldCount - newCount
		}
	}

	// Count added lines (lines in new but not matched in old).
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

	// Changed ratio relative to original file size.
	ratio := float64(changed) / float64(oldLines)

	// 60% threshold: a single edit changing >=60% of lines is suspicious.
	if ratio >= 0.60 {
		newLines := strings.Count(strings.TrimRight(newContent, "\n"), "\n") + 1
		return fmt.Sprintf(
			"This edit modified %d of %d lines (%.0f%% of the file): +%d added, -%d removed -> %d lines. "+
				"This is a high blast-radius change - verify this was intentional and not an overly broad old_text match or accidental rewrite. "+"For targeted changes, prefer edit_file with a more specific old_text anchor.",
			changed, oldLines, ratio*100, added, removed, newLines)
	}

	return ""
}
