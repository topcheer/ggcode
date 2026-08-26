package agent

// Logging Intelligence - Structured Logging & Observability Code Quality
//
// Problem: AI coding agents frequently introduce logging anti-patterns that
// degrade observability and leak sensitive data. Two high-impact patterns
// are NOT covered by existing checks:
//
//  1. Sensitive variables in log arguments: the agent writes
//     `log.Printf("auth token: %s", token)` or
//     `log.Info("user session", "password", password)`. The hardcoded-secret
//     check only catches literal values (e.g., `apiKey := "ghp_xxxx"`),
//     not DYNAMIC variable values passed as log arguments. This is a real
//     data-exfiltration vector: when the code runs, sensitive runtime values
//     are written to log files, stdout, or log aggregation systems.
//
//  2. log.Fatal/log.Panic in non-main packages: library code calling
//     log.Fatal() kills the entire process without giving the caller any
//     chance to handle the error gracefully. AI agents often copy-paste
//     log.Fatal patterns from main() into helper functions. This violates
//     the Go convention that library packages should return errors, not
//     terminate the process.
//
// Competitor analysis:
//   - Datadog/Sentry: runtime log monitoring, not write-time detection
//   - Semgrep: has rules for log.Fatal but requires external tool
//   - OpenTelemetry: standardizes structured logs but doesn't detect misuse
//   - Claude Code / Cursor / Cline: no write-time logging quality checks
//
// This check is zero-LLM-cost, delta-aware (only flags NEW patterns introduced
// by the edit), and <1ms per file using regex pattern matching.
//
// DIFFERENT from existing checks:
//   - debug_stmt_check.go: catches fmt.Println/console.log leftover debug prints
//     (bare print statements with no logging framework)
//   - hardcoded_secret_check.go: catches literal secret VALUES in source code
//     (static strings, not dynamic variables)
//   - THIS module: catches DYNAMIC sensitive variables passed to log calls
//     and log.Fatal/Panic misuse in library code

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// maxLogScanLen caps the content size scanned for logging patterns.
const maxLogScanLen = 256 * 1024

// maxLogIntelWarnings caps the number of warnings to prevent context overflow.
const maxLogIntelWarnings = 5

// logIntelExemptDirs lists directories where logging patterns are expected.
var logIntelExemptDirs = []string{
	"testdata/", "fixtures/", "mocks/", "__mocks__/",
	"vendor/", "third_party/",
}

// sensitiveLogVarPattern matches variable names that are likely to contain
// sensitive data. These are the same naming conventions used by security
// scanners. The pattern matches IDENTIFIERS containing password, secret,
// token, apiKey, api_key, credential, etc. (case-insensitive).
// #1098 Bug 1: added word boundaries (\b) to avoid matching substrings
// like tokenCount, maxTokens, tokenizerCount, etc.
var sensitiveLogVarPattern = regexp.MustCompile(
	`\b(?i)(password|passwd|secret|token|apikey|api_key|accesskey|access_key|` +
		`credential|privatekey|private_key|authheader|auth_header|bearer|` +
		`sessionkey|session_key|clientsecret|client_secret|refreshtoken|` +
		`refresh_token)\b`,
)

// goLogCallRe matches Go log package calls that accept format strings or
// key-value arguments. Captures the full call expression for argument analysis.
// Matches: log.Printf, log.Println, log.Print, log.Fatalf, log.Panicf, etc.
// Also matches structured logger patterns: logger.Info, log.Error, slog.Info
var goLogCallRe = regexp.MustCompile(
	`(?m)^\s*(?:log|logger|slog|logr|zap)\.[A-Z]\w*\s*\(`,
)

// goFatalInLibRe matches log.Fatal/log.Panic family calls in Go.
var goFatalInLibRe = regexp.MustCompile(
	`\b(?:log|logger)\.(Fatal(?:f|ln)?|Panic(?:f|ln)?)\s*\(`,
)

// jsConsoleSensitiveRe matches console.log/error/warn calls in JS/TS where
// a sensitive variable name appears as an argument.
var jsConsoleSensitiveRe = regexp.MustCompile(
	`console\.(log|error|warn|info|debug)\s*\(([^)]*)\)`,
)

// packageClauseRe checks if a Go file belongs to package main.
// #1098 Bug 3: strip comments first to handle files with license headers.
// Uses (?m) for multiline mode so ^ matches after comment lines.
var packageMainRe = regexp.MustCompile(`(?m)^\s*package\s+main\b`)

// loggingIntelInstance represents one detected logging anti-pattern.
type loggingIntelInstance struct {
	category string // "sensitive_log_arg" or "fatal_in_library"
	detail   string // human-readable description
}

// checkLoggingIntel detects logging anti-patterns introduced by this edit.
// Returns warnings for:
//   - Sensitive variable names passed as arguments to log calls
//   - log.Fatal/log.Panic in non-main packages
//
// Delta-aware: only flags patterns NEW to this edit.
func checkLoggingIntel(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if !isLogIntelSourceFile(ext) {
		return nil
	}

	// Skip exempt directories
	lowerPath := strings.ToLower(filePath)
	for _, dir := range logIntelExemptDirs {
		if strings.Contains(lowerPath, dir) {
			return nil
		}
	}

	scanNew := newContent
	if len(scanNew) > maxLogScanLen {
		scanNew = scanNew[:maxLogScanLen]
	}
	scanOld := oldContent
	if len(scanOld) > maxLogScanLen {
		scanOld = scanOld[:maxLogScanLen]
	}

	oldSensitive := countSensitiveLogArgs(scanOld, ext)
	oldFatal := countFatalInLib(scanOld, ext, filePath)
	newSensitive := findSensitiveLogArgs(scanNew, ext)
	newFatal := findFatalInLib(scanNew, ext, filePath)

	var warnings []string

	// Sensitive log args: flag only newly introduced instances
	if len(newSensitive) > oldSensitive {
		start := oldSensitive
		for i := start; i < len(newSensitive) && len(warnings) < maxLogIntelWarnings; i++ {
			warnings = append(warnings, newSensitive[i].detail)
		}
	}

	// Fatal in library: flag only newly introduced instances
	if len(newFatal) > oldFatal {
		start := oldFatal
		for i := start; i < len(newFatal) && len(warnings) < maxLogIntelWarnings; i++ {
			warnings = append(warnings, newFatal[i].detail)
		}
	}

	if len(warnings) > 0 {
		debug.Log("logging-intel", "detected %d logging anti-pattern(s) in %s", len(warnings), filePath)
	}

	return warnings
}

// isLogIntelSourceFile returns true for file types where logging patterns apply.
func isLogIntelSourceFile(ext string) bool {
	switch ext {
	case ".go", ".js", ".jsx", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

// findSensitiveLogArgs scans source code for log calls that pass sensitive
// variable names as arguments.
func findSensitiveLogArgs(src, ext string) []loggingIntelInstance {
	var results []loggingIntelInstance

	switch ext {
	case ".go":
		results = findGoSensitiveLogArgs(src)
	case ".js", ".jsx", ".ts", ".tsx":
		results = findJSSensitiveLogArgs(src)
	}

	return results
}

// findGoSensitiveLogArgs finds Go log calls with sensitive variable arguments.
func findGoSensitiveLogArgs(src string) []loggingIntelInstance {
	lines := strings.Split(src, "\n")
	var results []loggingIntelInstance

	for lineNum, line := range lines {
		if !goLogCallRe.MatchString(line) {
			continue
		}
		// Extract the arguments portion (between first '(' and matching ')')
		args := extractGoCallArgs(line)
		if args == "" {
			continue
		}
		// Check if any sensitive variable name appears in the arguments
		matches := sensitiveLogVarPattern.FindAllString(args, -1)
		if len(matches) == 0 {
			continue
		}
		// Filter out false positives: sensitive word appearing in a
		// string literal context that is NOT a format specifier target.
		// We only flag when the sensitive name appears as a bare identifier
		// (variable reference), not inside a quoted string.
		if hasSensitiveVarRef(args, matches) {
			results = append(results, loggingIntelInstance{
				category: "sensitive_log_arg",
				detail: fmt.Sprintf(
					"[LOGGING WARNING] Sensitive variable in log call at line %d: "+
						"`%s` passes sensitive data (%s) to a log function. "+
						"Logging sensitive runtime values (passwords, tokens, API keys) "+
						"is a data-exfiltration risk (OWASP A09:2021). Remove the "+
						"sensitive variable from log arguments or redact it before logging.",
					lineNum+1, truncateForLog(line, 80), strings.Join(matches, ", ")),
			})
		}
	}

	return results
}

// findJSSensitiveLogArgs finds JS/TS console calls with sensitive variable args.
func findJSSensitiveLogArgs(src string) []loggingIntelInstance {
	lines := strings.Split(src, "\n")
	var results []loggingIntelInstance

	for lineNum, line := range lines {
		matches := jsConsoleSensitiveRe.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			args := m[2]
			sensitiveMatches := sensitiveLogVarPattern.FindAllString(args, -1)
			if len(sensitiveMatches) == 0 {
				continue
			}
			if hasSensitiveVarRef(args, sensitiveMatches) {
				results = append(results, loggingIntelInstance{
					category: "sensitive_log_arg",
					detail: fmt.Sprintf(
						"[LOGGING WARNING] Sensitive variable in console.%s at line %d: "+
							"passes sensitive data (%s) to console output. "+
							"This is a data-exfiltration risk in browser/server logs. "+
							"Remove the sensitive variable or redact it before logging.",
						m[1], lineNum+1, strings.Join(sensitiveMatches, ", ")),
				})
			}
		}
	}

	return results
}

// hasSensitiveVarRef checks if sensitive names appear as bare identifiers
// (variable references) rather than only inside string literals.
// This reduces false positives where "password" appears in a log message string.
func hasSensitiveVarRef(args string, sensitiveMatches []string) bool {
	// Remove all quoted string contents, then check if sensitive names remain
	stripped := stripStringLiterals(args)
	for _, s := range sensitiveMatches {
		if strings.Contains(strings.ToLower(stripped), strings.ToLower(s)) {
			return true
		}
	}
	return false
}

// stripStringLiterals removes quoted string contents from a Go/JS expression,
// leaving only identifiers, operators, and punctuation.
func stripStringLiterals(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	var quote byte

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if c == '\\' && i+1 < len(s) {
				i++ // skip escaped char
				continue
			}
			if c == quote {
				inString = false
			}
			continue // skip chars inside string
		}
		if c == '"' || c == '\'' || c == '`' {
			inString = true
			quote = c
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// extractGoCallArgs extracts the content between the first '(' and the
// matching ')' in a line. Handles nested parens.
func extractGoCallArgs(line string) string {
	start := strings.Index(line, "(")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(line); i++ {
		switch line[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return line[start+1 : i]
			}
		}
	}
	return line[start+1:] // unterminated, return rest
}

// truncateForLog truncates a string for display in warning messages.
func truncateForLog(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// findFatalInLib finds log.Fatal/Panic calls in non-main Go packages.
// #1098 Bug 2: implemented init() filtering and comment stripping.
// Strips comments before matching to avoid false positives from comments.
func findFatalInLib(src, ext, filePath string) []loggingIntelInstance {
	if ext != ".go" {
		return nil
	}

	// Strip comments to avoid false positives from comments containing
	// log.Fatal references (e.g., "// do NOT call log.Fatal()")
	stripped := stripGoComments(src)

	// If this is package main, Fatal/Panic is acceptable
	if packageMainRe.MatchString(stripped) {
		return nil
	}

	// #1098 Bug 2: extract and exclude init() functions - log.Fatal in
	// init() is a common pattern for failing fast on misconfiguration.
	// Only flag Fatal in regular function bodies.
	stripped = stripInitFuncs(stripped)

	matches := goFatalInLibRe.FindAllString(stripped, -1)
	var results []loggingIntelInstance
	for range matches {
		results = append(results, loggingIntelInstance{
			category: "fatal_in_library",
			detail: "[LOGGING WARNING] log.Fatal/log.Panic in non-main package. " +
				"Calling log.Fatal in a library terminates the entire process " +
				"without giving callers a chance to handle the error. " +
				"Return the error instead and let the caller decide how to handle it. " +
				"(See Effective Go: 'Don't panic' / 'Errors are values'.)",
		})
	}
	return results
}

// #1098 Bug 2: stripGoComments removes single-line and multi-line Go comments
// from source code to avoid false positives from comment text.
func stripGoComments(src string) string {
	var result strings.Builder
	inSingleLineComment := false
	inMultiLineComment := false

	for i := 0; i < len(src); i++ {
		if inSingleLineComment {
			if src[i] == '\n' {
				inSingleLineComment = false
				result.WriteByte(src[i])
			}
			continue
		}

		if inMultiLineComment {
			if i+1 < len(src) && src[i] == '*' && src[i+1] == '/' {
				inMultiLineComment = false
				i++ // skip the closing '/'
				continue
			}
			continue
		}

		// Check for comment start
		if i+1 < len(src) && src[i] == '/' {
			if src[i+1] == '/' {
				inSingleLineComment = true
				i++ // skip the second '/'
				continue
			}
			if src[i+1] == '*' {
				inMultiLineComment = true
				i++ // skip the '*'
				continue
			}
		}

		// Not in a comment, copy the character
		result.WriteByte(src[i])
	}

	return result.String()
}

// #1098 Bug 2: stripInitFuncs removes init() function bodies from Go source code
// so log.Fatal inside init() is not flagged. Uses a simple regex-based approach
// that removes everything between "func init()" and the matching closing brace.
func stripInitFuncs(src string) string {
	// This regex matches "func init()" followed by any content until
	// the matching closing brace (balanced brace handling is simplified here)
	re := regexp.MustCompile(`func\s+init\s*\(\s*\)\s*\{[^}]*\}`)
	return re.ReplaceAllString(src, "")
}

// countSensitiveLogArgs returns the count of sensitive-log-arg patterns.
func countSensitiveLogArgs(src, ext string) int {
	return len(findSensitiveLogArgs(src, ext))
}

// countFatalInLib returns the count of fatal-in-library patterns.
func countFatalInLib(src, ext, filePath string) int {
	return len(findFatalInLib(src, ext, filePath))
}
