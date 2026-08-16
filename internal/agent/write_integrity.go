package agent

import (
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
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
	// maxIntegrityWarnings: maximum warnings to inject into the tool result.
	// Reduced from 3 to 1 - each warning wastes 100-300 tokens of context.
	// Only the single most critical issue should surface; the rest are
	// logged via debug.Log for offline analysis.
	maxIntegrityWarnings = 1
	maxGoSyntaxErrors    = 1
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
		// === CRITICAL CHECKS ONLY ===
		// Only checks that detect bugs causing crashes, data corruption,
		// security vulnerabilities, or silent wrong behavior are retained.
		// All advisory/style/performance checks have been removed to
		// eliminate context pollution (each check wastes 100-300 tokens).

		// --- File integrity ---
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
		{Name: "merge-conflict-markers", Run: stringCheckNew(checkMergeConflictMarkers)},

		// --- Go correctness (crashes, data races, leaks) ---
		// Re-registered per #328/#330: interface_compliance, deprecated_api,
		// printf_format, suspicious_comparison, dep_major_bump, dependency_vuln.
		{Name: "go-syntax", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			if ctx.GoAST != nil || strings.TrimSpace(ctx.NewContent) == "" {
				return nil
			}
			fset := token.NewFileSet()
			_, err := parser.ParseFile(fset, ctx.FilePath, ctx.NewContent, 0)
			return goSyntaxWarnings(ctx.FilePath, err)
		}},
		{Name: "nil-map-write", Langs: []Language{LangGo}, Run: stringCheck(checkNilMapWrite)},
		// #499: registered as whole-word slot — was a fully-implemented,
		// unit-tested detector with zero wiring (third instance of the
		// #328/#330 dead-detector class).
		{Name: "append-ignored", Langs: []Language{LangGo}, Run: sliceCheck(checkAppendIgnored)},
		// #503: there is deliberately NO "assertion-weakening" entry here.
		// checkAssertionWeakening (born 3129668f, unregistered by the
		// fc5c4aad critical-only refactor) was DELETED, not resurrected:
		// its position-unaware literal comparison treats human-readable
		// message strings ("failed to fetch" → "failed to fetch user
		// profile") as expected values, so re-registering it as-is would
		// fire reward-hacking accusations on everyday error-message edits
		// and eat the maxIntegrityWarnings budget. Do NOT re-add it here
		// without a position-aware exemption (testify trailing msgAndArgs,
		// t.Error*/t.Fatal* first-arg format strings).
		//
		// #506: same fate for "error-order" and "float-equality" (both
		// fc5c4aad-stripped, both empirically 100%-FP on revival — sa-165
		// probe-verified): checkErrorOrder fired on the io.Writer contract
		// idiom (n, err := w.Write(b); total += n — n is VALID on error by
		// contract, io.Copy does this) with a factually wrong "causes a
		// panic" claim for non-pointer results, and its surviving true
		// positives are already covered by nil-deref-after-error above
		// (which stays silent on the writer idiom); checkFloatEquality
		// matched EVERY math.* call as float64 (IsNaN/IsInf/Float64bits
		// return bool/uint64 — its epsilon fix advice does not compile)
		// and flagged exact-representable sentinels (== 0.5, == 1.0) where
		// epsilon comparison would BREAK correct code. Revival
		// preconditions: error-order → dereference-only uses + writer-
		// contract exemption (then it converges to nil-deref-after-error);
		// float-equality → function-name whitelist + exact-literal
		// exemption + delta wiring + shadowing guard (golangci-lint keeps
		// float-eq off by default for the same sentinel-vs-computed reason).
		//
		// #507: same fate for "error-swallow" and "error-nopropagate"
		// (both fc5c4aad-stripped, sa-166 probe-verified 4 FP classes).
		// error-swallow Pattern 2 (bare return) fired on named-result
		// propagation — `func f() (err error) { ...; if err != nil {
		// return } }` returns the NON-nil err, but the warning claimed
		// "instead of returning nil"; Pattern 1 (empty body) is the only
		// zero-FP revivable part. error-nopropagate fired on errors.Join
		// accumulation, struct-field stores (s.lastErr = err), and channel
		// handoffs (errCh <- err) — deferred-propagation sinks that AST
		// heuristics cannot distinguish from swallowing without
		// interprocedural dataflow (the reason go vet/errcheck/staticcheck
		// all deliberately implement no such check), and its line-number
		// delta re-reported every old instance after any top-of-file
		// insert. Revival preconditions: error-swallow → named-result
		// exemption + drop the "returning nil" claim (or revive Pattern 1
		// alone); error-nopropagate → deferred-sink exemptions (error-slice
		// append / field store / channel send / closure capture) +
		// fingerprint delta instead of line numbers. The live helpers
		// looksLikeError/isNilIdent were migrated to nil_deref_check.go.
		{Name: "concurrent-map-access", Langs: []Language{LangGo}, Run: stringCheck(checkConcurrentMapAccess)},
		{Name: "context-leak", Langs: []Language{LangGo}, Run: stringCheck(checkContextLeak)},
		{Name: "resource-leak", Langs: []Language{LangGo}, Run: sliceCheck(checkResourceLeaks)},
		{Name: "unchecked-type-assert", Langs: []Language{LangGo}, Run: sliceCheck(checkUncheckedTypeAssert)},
		{Name: "lock-without-unlock", Langs: []Language{LangGo}, Run: sliceCheck(checkLockWithoutUnlock)},
		{Name: "waitgroup-misuse", Langs: []Language{LangGo}, Run: sliceCheck(checkWaitGroupMisuse)},
		{Name: "copylock", Langs: []Language{LangGo}, Run: sliceCheck(checkCopylock)},
		{Name: "channel-safety", Langs: []Language{LangGo}, Run: sliceCheck(checkChannelSafety)},
		{Name: "slice-bounds-risk", Langs: []Language{LangGo}, Run: sliceCheck(checkSliceBoundsRisk)},
		{Name: "nil-deref-after-error", Langs: []Language{LangGo}, Run: stringCheck(checkNilDerefAfterError)},
		{Name: "range-nil-ptr", Langs: []Language{LangGo}, Run: stringCheck(checkRangeNilPtr)},
		{Name: "panic-safety", Langs: []Language{LangGo}, Run: sliceCheck(checkPanicSafety)},
		{Name: "retry-quality", Langs: []Language{LangGo}, Run: sliceCheck(checkRetryQuality)},

		// --- Security (OWASP / CVE-class) ---
		{Name: "sql-injection", Langs: []Language{LangGo}, Run: sliceCheck(checkSQLInjection)},
		{Name: "path-traversal", Langs: []Language{LangGo, LangJSTS, LangPython}, Run: sliceCheck(checkPathTraversal)},
		{Name: "sensitive-json", Langs: []Language{LangGo}, Run: sliceCheck(checkSensitiveJSONExposure)},
		{Name: "hardcoded-secret", Run: sliceCheck(checkHardcodedSecrets)},
		{Name: "insecure-patterns", Langs: []Language{LangGo, LangJSTS, LangPython}, Run: sliceCheck(checkInsecurePatterns)},

		// --- Security: supply chain (#330) ---
		{Name: "dep-major-bump", Run: stringCheck(checkBreakingChangeDepAsString)}, // all langs: self-filters by manifest filename
		{Name: "dependency-vuln", Run: stringCheck(checkDependencyVulnsAsString)},  // all langs: self-filters by manifest filename

		// --- Go correctness: API misuse / logic smells (#328/#330) ---
		{Name: "deprecated-api", Langs: []Language{LangGo}, Run: stringCheck(checkDeprecatedAPI)},
		{Name: "interface-compliance", Langs: []Language{LangGo}, Run: stringCheck(checkInterfaceCompliance)},
		{Name: "printf-format", Langs: []Language{LangGo}, Run: sliceCheck(checkPrintfFormat)},
		{Name: "suspicious-comparison", Langs: []Language{LangGo}, Run: stringCheck(checkSuspiciousComparison)},

		// --- Markup structural (breaks rendering) ---
		{Name: "tag-balance", Langs: []Language{LangMarkup, LangJSTS}, Run: stringCheckNew(checkTagBalance)},
		{Name: "delimiter-balance", Run: stringCheckNew(checkDelimiterBalance)},
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
