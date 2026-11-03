package agent

import (
	"fmt"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/diff"
)

const (
	maxIntegrityWarnings = 1
	maxGoSyntaxErrors    = 1
)

func checkWriteIntegrity(filePath, oldContent, newContent string) string {

	if disk, rerr := osReadFileForIntegrity(filePath); rerr == nil {
		if string(disk) != newContent {

			trimmed := string(disk)
			if len(trimmed) > 120 {
				trimmed = trimmed[:120] + "..."
			}
			mismatchMsg := fmt.Sprintf("post-write mismatch: file on disk differs from what was written (starts %q) - the write may be partial, or something (formatter/other agent) rewrote it immediately; re-read before further edits", trimmed)
			ctx := newCheckContext(filePath, oldContent, string(disk))
			if extra := formatWarnings(runChecksParallel(ctx)); extra != "" {
				return mismatchMsg + " | " + extra
			}
			return mismatchMsg
		}
	}
	ctx := newCheckContext(filePath, oldContent, newContent)
	warnings := runChecksParallel(ctx)
	return formatWarnings(warnings)
}

func osReadFileForIntegrity(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func stringCheck(fn func(string, string, string) string) func(CheckContext) []string {
	return func(ctx CheckContext) []string {
		if w := fn(ctx.FilePath, ctx.OldContent, ctx.NewContent); w != "" {
			return []string{w}
		}
		return nil
	}
}

func sliceCheck(fn func(string, string, string) []string) func(CheckContext) []string {
	return func(ctx CheckContext) []string {
		return fn(ctx.FilePath, ctx.OldContent, ctx.NewContent)
	}
}

func deltaGateNew(fn func(string, string) string) func(CheckContext) []string {
	return func(ctx CheckContext) []string {
		if !diff.HasChanges(ctx.OldContent, ctx.NewContent) {
			return nil
		}
		newW := fn(ctx.FilePath, ctx.NewContent)
		if newW == "" {
			return nil
		}

		if oldW := fn(ctx.FilePath, ctx.OldContent); normalizeIntegrityMsg(oldW) == normalizeIntegrityMsg(newW) {
			return nil
		}
		return []string{newW}
	}
}

func normalizeIntegrityMsg(msg string) string {
	if msg == "" {
		return ""
	}
	var b strings.Builder
	i := 0
	for i < len(msg) {

		if msg[i] >= '0' && msg[i] <= '9' && isLineRefPrefix(msg[:i]) {
			for i < len(msg) && msg[i] >= '0' && msg[i] <= '9' {
				i++
			}

			for i < len(msg) && (msg[i] == ',' || msg[i] == ' ') {
				j := i
				for j < len(msg) && (msg[j] == ',' || msg[j] == ' ') {
					j++
				}
				k := j
				for k < len(msg) && msg[k] >= '0' && msg[k] <= '9' {
					k++
				}
				if k > j {
					i = k
				} else {
					break
				}
			}
			b.WriteString("#")
			continue
		}
		b.WriteByte(msg[i])
		i++
	}
	return b.String()
}

func isLineRefPrefix(before string) bool {
	return strings.HasSuffix(before, "line ") || strings.HasSuffix(before, "lines ")
}

func mirrorWriteTimeGoFormat(path, content string) string {
	if filepath.Ext(path) != ".go" {
		return content
	}
	formatted, err := format.Source([]byte(content))
	if err != nil {
		return content
	}
	return string(formatted)
}

func registerAllChecks() {
	allChecks = []IntegrityCheck{

		{Name: "binary-corruption", Severity: SeverityCritical, Run: func(ctx CheckContext) []string {
			count := strings.Count(ctx.NewContent, "\x00")
			if count == 0 {
				return nil
			}
			oldCount := strings.Count(ctx.OldContent, "\x00")
			if oldCount >= count {
				return nil
			}
			if oldCount > 0 {
				return []string{fmt.Sprintf("This write introduced %d new null byte(s) (\\x00) - file now contains %d total. Content may be corrupted or incorrectly encoded. Check encoding and re-write if needed.", count-oldCount, count)}
			}
			return []string{fmt.Sprintf("File contains %d null byte(s) (\\x00) - content may be corrupted or incorrectly encoded. Check encoding and re-write if needed.", count)}
		}},
		{Name: "content-loss", Severity: SeverityCritical, Run: func(ctx CheckContext) []string {
			if strings.TrimSpace(ctx.OldContent) == "" || strings.TrimSpace(ctx.NewContent) != "" {
				return nil
			}
			return []string{fmt.Sprintf("This edit resulted in an EMPTY file (was %d bytes before). Verify this was intended - the old_text match may have consumed the entire file content.", len(ctx.OldContent))}
		}},
		{Name: "merge-conflict-markers", Severity: SeverityCritical, Run: deltaGateNew(checkMergeConflictMarkers)},
		{Name: "content-growth", Run: stringCheck(checkContentGrowth)},
		{Name: "edit-blast-radius", Run: stringCheck(checkEditBlastRadius)},

		{Name: "go-syntax", Langs: []Language{LangGo}, Severity: SeverityCritical, Run: func(ctx CheckContext) []string {
			if ctx.GoAST != nil || strings.TrimSpace(ctx.NewContent) == "" {
				return nil
			}
			fset := token.NewFileSet()
			_, err := parser.ParseFile(fset, ctx.FilePath, ctx.NewContent, 0)
			return goSyntaxWarnings(ctx.FilePath, err)
		}},
		{Name: "nil-map-write", Langs: []Language{LangGo}, Run: stringCheck(checkNilMapWrite)},

		{Name: "append-ignored", Langs: []Language{LangGo}, Run: sliceCheck(checkAppendIgnored)},

		{Name: "regex-loop", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			return checkRegexLoop(ctx.FilePath, ctx.OldContent, ctx.NewContent)
		}},
		{Name: "nplus1-loop", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			return checkNPlus1Loop(ctx.FilePath, ctx.OldContent, ctx.NewContent)
		}},

		{Name: "interface-design", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			return checkInterfaceDesign(ctx)
		}},

		{Name: "param-count", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			return checkExcessiveParams(ctx.FilePath, ctx.OldContent, ctx.NewContent)
		}},

		{Name: "return-count", Langs: []Language{LangGo}, Run: sliceCheck(checkExcessiveReturns)},

		{Name: "concurrent-map-access", Langs: []Language{LangGo}, Run: stringCheck(checkConcurrentMapAccess)},
		{Name: "context-leak", Langs: []Language{LangGo}, Run: stringCheck(checkContextLeak)},
		{Name: "resource-leak", Langs: []Language{LangGo}, Run: sliceCheck(checkResourceLeaks)},
		{Name: "unchecked-type-assert", Langs: []Language{LangGo}, Run: sliceCheck(checkUncheckedTypeAssert)},

		{Name: "type-switch-exhaustive", Langs: []Language{LangGo}, Run: sliceCheck(checkTypeSwitchExhaustive)},
		{Name: "lock-without-unlock", Langs: []Language{LangGo}, Run: sliceCheck(checkLockWithoutUnlock)},
		{Name: "waitgroup-misuse", Langs: []Language{LangGo}, Run: sliceCheck(checkWaitGroupMisuse)},
		{Name: "copylock", Langs: []Language{LangGo}, Run: sliceCheck(checkCopylock)},
		{Name: "channel-safety", Langs: []Language{LangGo}, Run: sliceCheck(checkChannelSafety)},
		{Name: "slice-bounds-risk", Langs: []Language{LangGo}, Run: sliceCheck(checkSliceBoundsRisk)},
		{Name: "nil-deref-after-error", Langs: []Language{LangGo}, Run: stringCheck(checkNilDerefAfterError)},

		{Name: "empty-error-body", Langs: []Language{LangGo}, Run: sliceCheck(checkEmptyErrorBody)},
		{Name: "range-nil-ptr", Langs: []Language{LangGo}, Run: stringCheck(checkRangeNilPtr)},
		{Name: "panic-safety", Langs: []Language{LangGo}, Run: sliceCheck(checkPanicSafety)},
		{Name: "retry-quality", Langs: []Language{LangGo}, Run: sliceCheck(checkRetryQuality)},

		{Name: "race-verify-hint", Langs: []Language{LangGo}, Severity: SeverityCritical, Run: sliceCheck(checkRaceVerifyHint)},

		{Name: "sql-injection", Langs: []Language{LangGo}, Severity: SeverityCritical, Run: sliceCheck(checkSQLInjection)},
		{Name: "path-traversal", Langs: []Language{LangGo, LangJSTS, LangPython}, Severity: SeverityCritical, Run: sliceCheck(checkPathTraversal)},

		{Name: "python-indentation", Langs: []Language{LangPython}, Severity: SeverityCritical, Run: deltaGateNew(checkPythonIndentation)},
		{Name: "sensitive-json", Langs: []Language{LangGo}, Severity: SeverityCritical, Run: sliceCheck(checkSensitiveJSONExposure)},
		{Name: "hardcoded-secret", Severity: SeverityCritical, Run: sliceCheck(checkHardcodedSecrets)},
		{Name: "insecure-patterns", Langs: []Language{LangGo, LangJSTS, LangPython}, Severity: SeverityCritical, Run: sliceCheck(checkInsecurePatterns)},

		{Name: "jsts-antipattern", Langs: []Language{LangJSTS}, Run: stringCheck(checkJSTSAntiPatterns)},

		{Name: "http-plaintext", Severity: SeverityCritical, Run: sliceCheck(checkHTTPPlaintext)},

		{Name: "logging-intel", Langs: []Language{LangGo, LangJSTS}, Severity: SeverityCritical, Run: sliceCheck(checkLoggingIntel)},

		{Name: "dep-major-bump", Severity: SeverityCritical, Run: stringCheck(checkBreakingChangeDepAsString)},
		{Name: "dependency-vuln", Severity: SeverityCritical, Run: stringCheck(checkDependencyVulnsAsString)},
		{Name: "typosquat", Severity: SeverityCritical, Run: stringCheck(checkTyposquattingAsString)},
		{Name: "new-dep-verify", Severity: SeverityDefault, Run: stringCheck(checkNewDependencyVerifyAsString)},

		{Name: "deprecated-api", Langs: []Language{LangGo}, Run: stringCheck(checkDeprecatedAPI)},
		{Name: "interface-compliance", Langs: []Language{LangGo}, Run: stringCheck(checkInterfaceCompliance)},
		{Name: "printf-format", Langs: []Language{LangGo}, Run: sliceCheck(checkPrintfFormat)},
		{Name: "suspicious-comparison", Langs: []Language{LangGo}, Run: stringCheck(checkSuspiciousComparison)},

		{Name: "value-recv-mutation", Langs: []Language{LangGo}, Run: sliceCheck(checkValueRecvMutation)},

		{Name: "unsafe-usage", Langs: []Language{LangGo}, Run: sliceCheck(checkUnsafeUsage)},

		{Name: "init-sideeffect", Langs: []Language{LangGo}, Run: sliceCheck(checkInitSideEffects)},

		{Name: "constant-conditional", Langs: []Language{LangGo}, Run: sliceCheck(checkConstantConditional)},

		{Name: "unreachable-code", Langs: []Language{LangGo}, Run: sliceCheck(checkUnreachableCode)},

		{Name: "magic-numbers", Langs: []Language{LangGo}, Run: stringCheck(checkMagicNumbers)},

		{Name: "map-prealloc", Langs: []Language{LangGo}, Run: sliceCheck(checkMapPrealloc)},

		{Name: "missing-prealloc", Langs: []Language{LangGo}, Run: sliceCheck(checkMissingPrealloc)},

		{Name: "test-isolation", Langs: []Language{LangGo}, Run: stringCheck(checkTestIsolation)},

		{Name: "unkeyed-struct", Langs: []Language{LangGo}, Run: sliceCheck(checkUnkeyedStruct)},

		{Name: "unused-param", Langs: []Language{LangGo}, Run: sliceCheck(checkUnusedParam)},

		{Name: "unicode-check", Langs: []Language{LangGo, LangPython, LangJSTS, LangMarkup, LangConfig, LangRuby, LangJava}, Run: stringCheck(checkUnicodeChars)},

		{Name: "hardcoded-output", Run: sliceCheck(checkHardcodedOutput)},

		{Name: "suppression-directives", Run: sliceCheck(checkSuppressionDirectives)},

		{Name: "placeholder-code", Run: sliceCheck(checkPlaceholderCode)},

		{Name: "assertion-presence", Langs: []Language{LangGo}, Run: stringCheck(checkAssertionPresence)},

		{Name: "tag-balance", Langs: []Language{LangMarkup, LangJSTS}, Run: deltaGateNew(checkTagBalance)},
		{Name: "delimiter-balance", Severity: SeverityCritical, Run: deltaGateNew(checkDelimiterBalance)},

		{Name: "config-syntax", Severity: SeverityCritical, Run: deltaGateNew(configSyntaxCheck)},

		{Name: "hardcoded-path", Run: sliceCheck(checkHardcodedPaths)},
	}

	debug.Log("integrity", "registered %d post-write checks", len(allChecks))
}

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

func checkGoSyntax(filename, src string) []string {
	_, _, err := parseGoSource(filename, src, 0)
	return goSyntaxWarnings(filename, err)
}

func checkEditBlastRadius(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(oldContent) == "" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	oldLines := strings.Count(strings.TrimRight(oldContent, "\n"), "\n") + 1
	if oldLines < 20 {
		return ""
	}

	trimmedOld := strings.TrimRight(oldContent, "\n")
	trimmedNew := strings.TrimRight(newContent, "\n")
	oldSet := make(map[string]int)
	for _, line := range strings.Split(trimmedOld, "\n") {
		oldSet[line]++
	}
	newSet := make(map[string]int)
	for _, line := range strings.Split(trimmedNew, "\n") {
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

	newLines := strings.Count(strings.TrimRight(newContent, "\n"), "\n") + 1
	denom := oldLines
	if newLines > denom {
		denom = newLines
	}
	ratio := float64(changed) / float64(denom)
	if ratio >= 0.60 {
		return fmt.Sprintf(
			"This edit modified %d of %d lines (%.0f%% of the file): +%d added, -%d removed -> %d lines. "+
				"This is a high blast-radius change - verify this was intentional and not an overly broad old_text match or accidental rewrite. "+
				"For targeted changes, prefer edit_file with a more specific old_text anchor.",
			changed, denom, ratio*100, added, removed, newLines)
	}

	return ""
}
