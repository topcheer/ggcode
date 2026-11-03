package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxLogScanLen = 256 * 1024

const maxLogIntelWarnings = 5

var logIntelExemptDirs = []string{
	"testdata/", "fixtures/", "mocks/", "__mocks__/",
	"vendor/", "third_party/",
}

var sensitiveLogVarPattern = regexp.MustCompile(
	`\b(?i)(password|passwd|secret|token|apikey|api_key|accesskey|access_key|` +
		`credential|privatekey|private_key|authheader|auth_header|bearer|` +
		`sessionkey|session_key|clientsecret|client_secret|refreshtoken|` +
		`refresh_token|` +

		`authtoken|accesstoken|idtoken|` +

		`[a-z0-9]+_token|[a-z0-9]+_secret|[a-z0-9]+_password|[a-z0-9]+_apikey)\b`,
)

var goLogCallRe = regexp.MustCompile(
	`\b(?:log|logger|slog|logr|zap)\.[A-Z]\w*\s*\(`,
)

var goFatalInLibRe = regexp.MustCompile(
	`\b(?:log|logger)\.(Fatal(?:f|ln)?|Panic(?:f|ln)?)\s*\(`,
)

var jsConsoleSensitiveRe = regexp.MustCompile(
	`console\.(log|error|warn|info|debug)\s*\(([^)]*)\)`,
)

var packageMainRe = regexp.MustCompile(`(?m)^\s*package\s+main\b`)

type loggingIntelInstance struct {
	category string
	detail   string
	key      string
}

func checkLoggingIntel(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if !isLogIntelSourceFile(ext) {
		return nil
	}

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

	sensitiveOldKeys := collectContentKeys(findSensitiveLogArgs(scanOld, ext))
	fatalOldKeys := collectContentKeys(findFatalInLib(scanOld, ext, filePath))
	newSensitive := findSensitiveLogArgs(scanNew, ext)
	newFatal := findFatalInLib(scanNew, ext, filePath)

	var warnings []string

	warnings = warnNewInstances(newSensitive, sensitiveOldKeys, warnings)

	warnings = warnNewInstances(newFatal, fatalOldKeys, warnings)

	if len(warnings) > 0 {
		debug.Log("logging-intel", "detected %d logging anti-pattern(s) in %s", len(warnings), filePath)
	}

	return warnings
}

func collectContentKeys(insts []loggingIntelInstance) map[string]bool {
	keys := make(map[string]bool, len(insts))
	for _, inst := range insts {
		keys[normalizeLogCallKey(inst)] = true
	}
	return keys
}

func warnNewInstances(newInsts []loggingIntelInstance, oldKeys map[string]bool, warnings []string) []string {
	for i := range newInsts {
		if len(warnings) >= maxLogIntelWarnings {
			break
		}
		if oldKeys[normalizeLogCallKey(newInsts[i])] {
			continue
		}
		warnings = append(warnings, newInsts[i].detail)
	}
	return warnings
}

func isLogIntelSourceFile(ext string) bool {
	switch ext {
	case ".go", ".js", ".jsx", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

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

func findGoSensitiveLogArgs(src string) []loggingIntelInstance {

	src = stripGoComments(src)

	lines := strings.Split(src, "\n")
	var results []loggingIntelInstance

	for lineNum, line := range lines {

		for _, loc := range goLogCallRe.FindAllStringIndex(line, -1) {
			args := extractGoCallArgsAt(line, loc[1]-1)
			if args == "" {
				continue
			}

			matches := sensitiveLogVarPattern.FindAllString(args, -1)
			if len(matches) == 0 {
				continue
			}

			if hasSensitiveVarRef(args, matches, false) {
				results = append(results, loggingIntelInstance{
					category: "sensitive_log_arg",

					key: args,
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
	}

	return results
}

var jsRedactWrapperRe = regexp.MustCompile(
	`\b(?:redact|redacted|mask|masked|sanitize|sanitized|scrub|scrubbed|obfuscate|obfuscated)\s*\([^()]*\)`,
)

var jsConsoleCallRe = regexp.MustCompile(
	`console\.(log|error|warn|info|debug)\s*\(`,
)

func findJSSensitiveLogArgs(src string) []loggingIntelInstance {

	src = stripGoComments(src)

	lines := strings.Split(src, "\n")
	var results []loggingIntelInstance

	for lineNum, line := range lines {
		for _, loc := range jsConsoleCallRe.FindAllStringSubmatchIndex(line, -1) {

			args := extractGoCallArgsAt(line, loc[1]-1)
			if args == "" {
				continue
			}
			method := line[loc[2]:loc[3]]
			sensitiveMatches := sensitiveLogVarPattern.FindAllString(args, -1)
			if len(sensitiveMatches) == 0 {
				continue
			}
			if hasSensitiveVarRef(args, sensitiveMatches, true) {
				results = append(results, loggingIntelInstance{
					category: "sensitive_log_arg",

					key: method + "(" + args + ")",
					detail: fmt.Sprintf(
						"[LOGGING WARNING] Sensitive variable in console.%s at line %d: "+
							"passes sensitive data (%s) to console output. "+
							"This is a data-exfiltration risk in browser/server logs. "+
							"Remove the sensitive variable or redact it before logging.",
						method, lineNum+1, strings.Join(sensitiveMatches, ", ")),
				})
			}
		}
	}

	return results
}

func hasSensitiveVarRef(args string, sensitiveMatches []string, isJS bool) bool {

	stripped := stripStringLiteralsFor(args, isJS)

	stripped = jsRedactWrapperRe.ReplaceAllString(stripped, "")
	for _, s := range sensitiveMatches {
		if containsWordBoundary(strings.ToLower(stripped), strings.ToLower(s)) {
			return true
		}
	}
	return false
}

func containsWordBoundary(s, substr string) bool {
	for i := 0; ; {
		idx := strings.Index(s[i:], substr)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(substr)
		beforeOK := start == 0 || !isWordByte(s[start-1])
		afterOK := end == len(s) || !isWordByte(s[end])
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
	}
}

func stripStringLiteralsFor(s string, jsTemplate bool) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	var quote byte

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == quote {
				inString = false
			}

			if jsTemplate && quote == '`' && c == '$' && i+1 < len(s) && s[i+1] == '{' {
				j := i + 2
				depth := 1
				for j < len(s) && depth > 0 {
					if s[j] == '{' {
						depth++
					} else if s[j] == '}' {
						depth--
					}
					j++
				}
				expr := s[i+2 : j-1]
				for _, ch := range expr {
					b.WriteRune(ch)
				}
				b.WriteRune(' ')
				i = j - 1
			}
			continue
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

const maxLogInstKeyLen = 192

func normalizeLogCallKey(inst loggingIntelInstance) string {
	k := normalizeCallText(inst.key)
	if len(k) > maxLogInstKeyLen {
		k = k[:maxLogInstKeyLen]
	}
	return inst.category + "|" + k
}

func normalizeCallText(call string) string {
	var b strings.Builder
	var buf strings.Builder
	b.Grow(len(call))
	for i := 0; i < len(call); i++ {
		c := call[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			buf.Reset()
			i = consumeGoString(call, i, &buf) - 1
			b.WriteString(normalizeLiteralText(buf.String()))
		case c <= ' ':
		case c >= '0' && c <= '9':
			b.WriteByte('0')
			for i+1 < len(call) && call[i+1] >= '0' && call[i+1] <= '9' {
				i++
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func normalizeLiteralText(s string) string {
	var b strings.Builder
	b.WriteString("{S:")
	prevDigit := false
	for _, r := range s {
		if r <= ' ' {
			continue
		}
		if r >= '0' && r <= '9' {
			if !prevDigit {
				b.WriteByte('0')
				prevDigit = true
			}
			continue
		}
		prevDigit = false
		b.WriteRune(r)
	}
	b.WriteByte('}')
	return b.String()
}

func extractGoCallArgsAt(src string, openIdx int) string {
	if openIdx < 0 || openIdx >= len(src) || src[openIdx] != '(' {
		return ""
	}
	var sink strings.Builder
	depth := 1
	i := openIdx + 1
	for ; i < len(src); i++ {
		c := src[i]
		if c == '"' || c == '\'' || c == '`' {
			sink.Reset()
			i = consumeGoString(src, i, &sink) - 1
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return src[openIdx+1 : i]
			}
		}
	}
	return ""
}

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
	return line[start+1:]
}

func truncateForLog(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func findFatalInLib(src, ext, filePath string) []loggingIntelInstance {
	if ext != ".go" {
		return nil
	}

	stripped := stripGoComments(src)

	if packageMainRe.MatchString(stripped) {
		return nil
	}

	stripped = stripInitFuncs(stripped)

	offsets := goFatalInLibRe.FindAllStringIndex(stripped, -1)
	var results []loggingIntelInstance
	for _, off := range offsets {
		args := extractGoCallArgsAt(stripped, off[1]-1)
		results = append(results, loggingIntelInstance{
			category: "fatal_in_library",
			key:      stripped[off[0]:off[1]-1] + "(" + args + ")",
			detail: "[LOGGING WARNING] log.Fatal/log.Panic in non-main package. " +
				"Calling log.Fatal in a library terminates the entire process " +
				"without giving callers a chance to handle the error. " +
				"Return the error instead and let the caller decide how to handle it. " +
				"(See Effective Go: 'Don't panic' / 'Errors are values'.)",
		})
	}
	return results
}

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
				i = len(src)
			} else {
				i += 2 + rest
				result.WriteByte('\n')
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := consumeGoBlockComment(src, i, &result)
			if end < 0 {
				i = len(src)
			} else {
				i = end - 1
			}
		default:
			result.WriteByte(c)
		}
	}
	return result.String()
}

func consumeGoString(src string, i int, out *strings.Builder) int {
	quote := src[i]
	raw := quote == '`'
	out.WriteByte(quote)
	for j := i + 1; j < len(src); j++ {
		out.WriteByte(src[j])
		if !raw && src[j] == '\\' && j+1 < len(src) {
			j++
			out.WriteByte(src[j])
			continue
		}
		if src[j] == quote {
			return j + 1
		}
	}
	return len(src)
}

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

var initFuncDeclRe = regexp.MustCompile(`func\s+init\s*\(\s*\)\s*\{`)

func stripInitFuncs(src string) string {
	var b strings.Builder
	var sink strings.Builder
	b.Grow(len(src))
	writePos := 0
	removedUntil := 0
	for _, loc := range initFuncDeclRe.FindAllStringSubmatchIndex(src, -1) {
		start := loc[0]
		openBrace := loc[1] - 1
		if start < removedUntil {
			continue
		}
		depth := 1
		j := openBrace + 1
		for j < len(src) && depth > 0 {
			switch c := src[j]; {
			case c == '"' || c == '\'' || c == '`':
				sink.Reset()
				j = consumeGoString(src, j, &sink)
				continue
			case c == '{':
				depth++
			case c == '}':
				depth--
			}
			j++
		}
		if depth != 0 {
			continue
		}
		b.WriteString(src[writePos:start])
		writePos = j
		removedUntil = j
	}
	b.WriteString(src[writePos:])
	return b.String()
}
