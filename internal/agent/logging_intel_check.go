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
	line     int    // 1-based line in stripped source; disambiguates fatal-family instances whose details are identical (#1109 Item B)
}

// contentKey returns a stable identity key for the content-anchored delta
// comparison (#1109 Item B, mirroring the #1099 approach in
// lock_without_unlock_check.go). Fatal-family instances all share one detail
// string with no positional info, so their captured line breaks the tie;
// sensitive-log instances already embed line number and code excerpt.
func (li loggingIntelInstance) contentKey() string {
	if li.line > 0 {
		return fmt.Sprintf("%s|%d", li.category, li.line)
	}
	return li.category + "|" + li.detail
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

	// #1109 Item B: compare by instance identity keys (content anchors), not
	// by ordered-prefix position, so deleting one old instance while adding
	// two new ones flags both additions.
	sensitiveOldKeys := collectContentKeys(findSensitiveLogArgs(scanOld, ext))
	fatalOldKeys := collectContentKeys(findFatalInLib(scanOld, ext, filePath))
	newSensitive := findSensitiveLogArgs(scanNew, ext)
	newFatal := findFatalInLib(scanNew, ext, filePath)

	var warnings []string

	// Sensitive log args: flag only instances NEW relative to old keys.
	warnings = warnNewInstances(newSensitive, sensitiveOldKeys, warnings)

	// Fatal in library: same content-anchored delta (#1109 Item B).
	warnings = warnNewInstances(newFatal, fatalOldKeys, warnings)

	if len(warnings) > 0 {
		debug.Log("logging-intel", "detected %d logging anti-pattern(s) in %s", len(warnings), filePath)
	}

	return warnings
}

// collectContentKeys builds the identity-key set for a list of instances
// (#1109 Item B). Keys anchor each instance by content so the old/new delta
// survives deletions and reordering.
func collectContentKeys(insts []loggingIntelInstance) map[string]bool {
	keys := make(map[string]bool, len(insts))
	for _, inst := range insts {
		keys[inst.contentKey()] = true
	}
	return keys
}

// warnNewInstances appends details for instances whose content key did NOT
// exist before the edit (#1109 Item B). Stops once maxLogIntelWarnings is
// reached.
func warnNewInstances(newInsts []loggingIntelInstance, oldKeys map[string]bool, warnings []string) []string {
	for i := range newInsts {
		if len(warnings) >= maxLogIntelWarnings {
			break
		}
		if oldKeys[newInsts[i].contentKey()] {
			continue
		}
		warnings = append(warnings, newInsts[i].detail)
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
	// #1109 Item A2: strip comments before matching. A log.Printf wrapped in a
	// block comment previously produced a sensitive_log_arg false positive;
	// single-line // cases were already safe thanks to the ^\s* line anchor.
	// stripGoComments preserves newlines so reported line numbers stay valid.
	src = stripGoComments(src)

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

	// #1109 Item B: capture offsets so each instance carries a distinguishable
	// line key. Fatal details are otherwise all identical, making them
	// indistinguishable in the content-anchored delta.
	offsets := goFatalInLibRe.FindAllStringIndex(stripped, -1)
	var results []loggingIntelInstance
	for _, off := range offsets {
		line := 1 + strings.Count(stripped[:off[0]], "\n")
		results = append(results, loggingIntelInstance{
			category: "fatal_in_library",
			line:     line,
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
// #1109 Item A1: string, rune, and raw-string literals are tracked with a
// state machine so comment markers inside string values (e.g. URLs like
// "http://example.com" or template placeholders like "/* x */") no longer
// start a bogus comment region that silently swallows real log.Fatal code
// after it (under-reporting).
// #1109 Item A2: newlines consumed inside comments are preserved so line
// numbers reported downstream stay aligned with the original source.
func stripGoComments(src string) string {
	var result strings.Builder
	result.Grow(len(src))
	for i := 0; i < len(src); i++ {
		switch c := src[i]; {
		case c == '"' || c == '\'' || c == '`':
			i = consumeGoString(src, i, &result) - 1
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			rest := strings.IndexByte(src[i+2:], '\n')
			if rest < 0 {
				i = len(src) // line comment runs to EOF: drop silently
			} else {
				i += 2 + rest
				result.WriteByte('\n') // preserve the line break (#1109 Item A2)
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := consumeGoBlockComment(src, i, &result)
			if end < 0 {
				i = len(src) // unterminated comment: drop the remainder
			} else {
				i = end - 1
			}
		default:
			result.WriteByte(c)
		}
	}
	return result.String()
}

// consumeGoString copies the string/rune/raw-string literal starting at
// src[i] (which must be a quote character) verbatim into out and returns the
// index just past its closing quote, or len(src) if unterminated
// (#1109 Item A1).
func consumeGoString(src string, i int, out *strings.Builder) int {
	quote := src[i]
	raw := quote == '`' // backtick raw string - escapes carry no meaning
	out.WriteByte(quote)
	for j := i + 1; j < len(src); j++ {
		out.WriteByte(src[j])
		if !raw && src[j] == '\\' && j+1 < len(src) {
			j++
			out.WriteByte(src[j]) // escaped pair travels together
			continue
		}
		if src[j] == quote {
			return j + 1
		}
	}
	return len(src)
}

// consumeGoBlockComment skips a /* */ comment beginning at the '/' in src[i],
// writing interior newlines to out so line numbers stay aligned (#1109 Item
// A2). Returns the index just past the closing '*/', or -1 if unterminated.
func consumeGoBlockComment(src string, i int, out *strings.Builder) int {
	for j := i + 2; j < len(src); j++ {
		switch src[j] {
		case '\n':
			out.WriteByte('\n')
		case '*':
			if j+1 < len(src) && src[j+1] == '/' {
				return j + 2
			}
		}
	}
	return -1
}

// initFuncDeclRe matches the start of an init() function declaration up to its
// opening brace.
var initFuncDeclRe = regexp.MustCompile(`func\s+init\s*\(\s*\)\s*\{`)

// #1098 Bug 2 / #1109 Item C: stripInitFuncs removes init() function bodies
// from Go source code so log.Fatal inside init() is not flagged. Uses balanced
// brace scanning: the previous regex `[^}]*\}` stopped at the FIRST closing
// brace, truncating bodies that contain nested blocks (e.g. if statements) and
// leaving log.Fatal calls after those blocks unstripped (fail-fast false
// positives).
func stripInitFuncs(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	writePos := 0
	removedUntil := 0
	for _, loc := range initFuncDeclRe.FindAllStringSubmatchIndex(src, -1) {
		start := loc[0]
		openBrace := loc[1] - 1 // '{' is the last character of the match
		if start < removedUntil {
			continue // candidate sits inside an already removed init body
		}
		depth := 1
		j := openBrace + 1
		for j < len(src) && depth > 0 {
			switch src[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
			j++
		}
		if depth != 0 {
			continue // unterminated init body - leave untouched
		}
		b.WriteString(src[writePos:start])
		writePos = j
		removedUntil = j
	}
	b.WriteString(src[writePos:])
	return b.String()
}

// countSensitiveLogArgs returns the count of sensitive-log-arg patterns.
func countSensitiveLogArgs(src, ext string) int {
	return len(findSensitiveLogArgs(src, ext))
}

// countFatalInLib returns the count of fatal-in-library patterns.
func countFatalInLib(src, ext, filePath string) int {
	return len(findFatalInLib(src, ext, filePath))
}
