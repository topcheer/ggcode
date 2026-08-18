package agent

// Insecure Code Pattern Detection in File Writes
//
// Research basis: OWASP LLM Top 10 2025 (#5: Insecure Output Handling, and
// broader insecure code generation concerns). AI coding agents frequently
// generate code with security anti-patterns because they optimize for "working
// example" rather than "secure by default". The most common patterns:
//
//  1. TLS verification bypass: `InsecureSkipVerify: true` - disables certificate
//     validation, enabling MITM attacks. LLMs add this to "make HTTPS work"
//     when encountering self-signed certs or expired certificates.
//  2. Weak crypto for security operations: `math/rand` for tokens/passwords/keys
//     (predictable, not cryptographically secure - must use crypto/rand).
//     MD5/SHA1 for password hashing (broken, collision-vulnerable).
//  3. SQL injection: string concatenation or fmt.Sprintf in SQL queries instead
//     of parameterized queries. The #1 web vulnerability (OWASP A03:2021).
//  4. Command injection: exec.Command/shell with concatenated user input instead
//     of argument arrays.
//
// Competitor analysis:
//   - Claude Code: no write-time security pattern detection
//   - Cursor: relies on external linters (staticcheck, eslint-plugin-security)
//   - Cline/OpenHands: no detection
//   - GitHub Copilot: has some AI-based vulnerability filtering on suggestions
//   - Aider: no detection
//
// ggcode's approach: lightweight AST + text pattern matching that runs in <1ms
// at write time. Delta-aware: only flags patterns INTRODUCED by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// insecureVerifyDisabledRe matches TLS verification being disabled in Python
// code: verify=False / verify=0 (requests, httpx) and ssl=False / ssl_verify
// variants (aiohttp TCPConnector, urllib3). Fix #245: the previous
// three-condition substring AND missed httpx/aiohttp calls without a
// "requests"/"ssl"/"session" context word, and `verify=0` without spaces.
// The assignment itself is a strong enough signal for a security detector —
// prefer a false positive over a missed TLS bypass.
var insecureVerifyDisabledRe = regexp.MustCompile(`(?i)\b(?:ssl_verify|verify|ssl)\s*=\s*(?:false|0)\b`)

// insecureRejectUnauthorizedRe matches the Node.js TLS bypass assignment
// `rejectUnauthorized: false` (or `: 0`). Fix #245: a loose
// `Contains(lower, "0")` matched any zero on the line (e.g. `timeout: 0`).
var insecureRejectUnauthorizedRe = regexp.MustCompile(`(?i)rejectUnauthorized\s*:\s*(?:false|0)\b`)

// insecureNodeTLSDisabledRe matches the global Node.js TLS kill switch being
// ASSIGNED a disabling value. Comparisons (`=== '0'`, `!== '0'`) cannot match
// here: after NODE_TLS_REJECT_UNAUTHORIZED the regex requires `=` immediately
// (modulo whitespace) followed by 0/false, so `===` and `!==` fail to match.
// Fix #245: the substring check flagged legitimate comparison guards.
var insecureNodeTLSDisabledRe = regexp.MustCompile(`(?i)NODE_TLS_REJECT_UNAUTHORIZED\s*=\s*['"]?(?:0|false)['"]?`)

// insecurePatternInstance represents a detected insecure pattern.
type insecurePatternInstance struct {
	category string // "TLS bypass", "weak crypto", "SQL injection", "command injection"
	detail   string // specific detail about what was found
	line     int    // line number (0 if unknown)
}

// checkInsecurePatterns detects security anti-patterns introduced by this edit.
// Returns warning strings. Delta-aware: only flags NEW instances.
func checkInsecurePatterns(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := filepath.Ext(filePath)
	switch ext {
	case ".go":
		return checkInsecurePatternsGo(filePath, oldContent, newContent)
	case ".js", ".ts", ".jsx", ".tsx", ".mjs":
		return checkInsecurePatternsJS(filePath, oldContent, newContent)
	case ".py":
		return checkInsecurePatternsPython(filePath, oldContent, newContent)
	default:
		return nil
	}
}

// ---- Go detection (AST-based where possible, text fallback) ----

func checkInsecurePatternsGo(filePath, oldContent, newContent string) []string {
	oldIssues := findInsecurePatternsGo(oldContent)
	newIssues := findInsecurePatternsGo(newContent)

	// Only report issues that are genuinely new (by category+detail signature).
	oldSet := make(map[string]bool, len(oldIssues))
	for _, oi := range oldIssues {
		oldSet[oi.category+"|"+oi.detail] = true
	}

	var warnings []string
	reported := 0
	for _, ni := range newIssues {
		key := ni.category + "|" + ni.detail
		if oldSet[key] {
			continue
		}
		oldSet[key] = true // dedup within new issues too
		loc := ""
		if ni.line > 0 {
			loc = fmt.Sprintf(" (line %d)", ni.line)
		}
		warnings = append(warnings, fmt.Sprintf(
			"[Security] %s%s: %s. Review and use a secure alternative.",
			ni.category, loc, ni.detail))
		reported++
		if reported >= 3 {
			break
		}
	}
	return warnings
}

func findInsecurePatternsGo(content string) []insecurePatternInstance {
	var issues []insecurePatternInstance

	// Text-based checks (fast, always available).
	lines := strings.Split(content, "\n")
	inBlockComment := false // fix #728: /* */ block comment state across lines
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Fix #723: strip comments once for ALL text checks below (previously
		// only the SQL check did this, fix #278). Full-line comments are skipped
		// entirely; trailing comments are stripped, so every check (TLS bypass,
		// weak crypto, SQL, command injection) operates on code only and no
		// longer fires on comment mentions like
		// `// do not use InsecureSkipVerify: true`.
		// Fix #728: block-comment BODY lines (` * InsecureSkipVerify: true ...`)
		// start with neither `//` nor `/*` and previously slipped through; the
		// block state machine below skips them until `*/`.
		code, ok := cStyleBlockCommentLine(trimmed, &inBlockComment)
		if !ok {
			continue
		}
		trimmed = goStripTrailingComment(code)

		// 1. InsecureSkipVerify: true
		if strings.Contains(trimmed, "InsecureSkipVerify") &&
			(strings.Contains(trimmed, "true") || strings.Contains(trimmed, "True")) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "InsecureSkipVerify: true disables TLS certificate verification",
				line:     i + 1,
			})
		}

		// 2a. Weak hash for password: md5.New() or sha1.New() in context of password/token
		lowerLine := strings.ToLower(trimmed)
		if (strings.Contains(lowerLine, "md5.new()") || strings.Contains(lowerLine, "sha1.new()")) &&
			(strings.Contains(lowerLine, "password") || strings.Contains(lowerLine, "token") ||
				strings.Contains(lowerLine, "secret") || strings.Contains(lowerLine, "hash")) {
			issues = append(issues, insecurePatternInstance{
				category: "weak crypto",
				detail:   "MD5/SHA1 used for password/token hashing - use bcrypt, scrypt, or SHA-256+",
				line:     i + 1,
			})
		}

		// 3. SQL injection via string concatenation: "SELECT" + variable or fmt.Sprintf with SELECT.
		// Comments are already stripped above (fix #723 generalizes fix #278):
		// `a := b + c // SELECT count FROM users` must not count the comment's
		// keywords as the query.
		// Fix #245: `i++` and `x += y` are not concatenation and must not count.
		upperLine := strings.ToUpper(trimmed)
		if isSQLKeywordLine(upperLine) {
			// Check for concatenation or Sprintf in the same line
			if lineHasConcatPlus(trimmed) || strings.Contains(trimmed, "Sprintf") {
				issues = append(issues, insecurePatternInstance{
					category: "SQL injection",
					detail:   "SQL query built with string concatenation/Sprintf - use parameterized queries (? placeholders)",
					line:     i + 1,
				})
			}
		}

		// 4. Command injection: exec.Command with shell + concatenation
		if strings.Contains(trimmed, "exec.Command") {
			if strings.Contains(trimmed, "\"sh\"") || strings.Contains(trimmed, "\"bash\"") ||
				strings.Contains(trimmed, "\"/bin/sh\"") || strings.Contains(trimmed, "\"/bin/bash\"") {
				if strings.Contains(trimmed, "+") || strings.Contains(trimmed, "Sprintf") {
					issues = append(issues, insecurePatternInstance{
						category: "command injection",
						detail:   "exec.Command with shell and concatenated input - pass arguments as separate args",
						line:     i + 1,
					})
				}
			}
		}
	}

	// AST-based check: math/rand used for security-sensitive identifiers.
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, "", content, 0)
	if err == nil {
		// Build import alias -> package path map (fix #243). The previous
		// text matching (`strings.Contains(fnName, "rand.Read")` plus a
		// `!strings.Contains(fnName, "crypto")` exclusion) misclassified
		// aliased imports: `import crand "crypto/rand"` yields the selector
		// "crand.Read", which contains "rand.Read" but not "crypto". We now
		// resolve the selector's package identifier against the file's
		// imports and match exact package paths. When the import cannot be
		// resolved we skip — prefer a false negative over flagging secure
		// code in a security review warning.
		imports := make(map[string]string)
		for _, imp := range tree.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			name := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}
			imports[name] = path
		}

		ast.Inspect(tree, func(n ast.Node) bool {
			// Detect math/rand calls assigned to token/key/secret/password variables.
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, expr := range assign.Rhs {
				call, ok := expr.(*ast.CallExpr)
				if !ok {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				// math/rand functions of interest: Read, Intn, Int63, Int31, Float64.
				switch sel.Sel.Name {
				case "Read", "Intn", "Int63", "Int31", "Float64":
				default:
					continue
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok {
					continue
				}
				pkgPath, known := imports[pkgIdent.Name]
				if !known || pkgPath != "math/rand" {
					// Unresolvable import, or crypto/rand (secure): skip.
					continue
				}

				// Check if the LHS variable name suggests security sensitivity.
				for _, lhs := range assign.Lhs {
					ident, ok := lhs.(*ast.Ident)
					if !ok {
						continue
					}
					nameLower := strings.ToLower(ident.Name)
					if isSecuritySensitiveName(nameLower) {
						issues = append(issues, insecurePatternInstance{
							category: "weak crypto",
							detail:   fmt.Sprintf("math/rand used for security-sensitive variable '%s' - use crypto/rand", ident.Name),
							line:     fset.Position(assign.Pos()).Line,
						})
					}
				}
			}
			return true
		})
	}

	return issues
}

// ---- JavaScript/TypeScript detection (text-based) ----

func checkInsecurePatternsJS(filePath, oldContent, newContent string) []string {
	oldIssues := findInsecurePatternsJS(oldContent)
	newIssues := findInsecurePatternsJS(newContent)

	oldSet := make(map[string]bool, len(oldIssues))
	for _, oi := range oldIssues {
		oldSet[oi.category+"|"+oi.detail] = true
	}

	var warnings []string
	reported := 0
	for _, ni := range newIssues {
		key := ni.category + "|" + ni.detail
		if oldSet[key] {
			continue
		}
		oldSet[key] = true
		loc := ""
		if ni.line > 0 {
			loc = fmt.Sprintf(" (line %d)", ni.line)
		}
		warnings = append(warnings, fmt.Sprintf(
			"[Security] %s%s: %s. Review and use a secure alternative.",
			ni.category, loc, ni.detail))
		reported++
		if reported >= 3 {
			break
		}
	}
	return warnings
}

func findInsecurePatternsJS(content string) []insecurePatternInstance {
	var issues []insecurePatternInstance
	lines := strings.Split(content, "\n")
	inBlockComment := false // fix #728: /* */ block comment state across lines

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Fix #723: strip comments once for ALL checks below. JS shares Go's
		// `//` and `/*` comment syntax, so the Go trailing-comment stripper is
		// reused; full-line comments are skipped entirely. Comment mentions
		// like `// eval(userInput) is dangerous` no longer trigger.
		// Fix #728: block-comment BODY lines (` * eval(...)` etc.) previously
		// survived the `//`/`/*` prefix check; the block state machine skips them.
		code, ok := cStyleBlockCommentLine(trimmed, &inBlockComment)
		if !ok {
			continue
		}
		trimmed = goStripTrailingComment(code)

		lower := strings.ToLower(trimmed)

		// rejectUnauthorized: false / 0 (Node.js TLS bypass). Fix #245: match the
		// actual assignment shape so `timeout: 0` on the same line no longer trips it.
		if insecureRejectUnauthorizedRe.MatchString(trimmed) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "rejectUnauthorized: false disables TLS certificate verification",
				line:     i + 1,
			})
		}

		// process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0' (assignment only;
		// comparison guards like `=== '0'` must not warn — fix #245)
		if insecureNodeTLSDisabledRe.MatchString(trimmed) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "NODE_TLS_REJECT_UNAUTHORIZED=0 disables all TLS verification globally",
				line:     i + 1,
			})
		}

		// eval() usage - code injection risk
		if strings.Contains(lower, "eval(") && !strings.Contains(lower, "evaluate") {
			issues = append(issues, insecurePatternInstance{
				category: "code injection",
				detail:   "eval() executes arbitrary code - avoid or use JSON.parse for data",
				line:     i + 1,
			})
		}

		// innerHTML assignment with concatenation - XSS risk
		if strings.Contains(lower, "innerhtml") && (strings.Contains(trimmed, "+") ||
			strings.Contains(lower, "template") || strings.Contains(lower, "${")) {
			issues = append(issues, insecurePatternInstance{
				category: "XSS",
				detail:   "innerHTML with dynamic content - XSS risk, use textContent or sanitize",
				line:     i + 1,
			})
		}

		// Math.random() for security-sensitive operations
		if strings.Contains(lower, "math.random()") {
			// Check if nearby variable name is security-sensitive
			if isLineSecuritySensitive(lower) {
				issues = append(issues, insecurePatternInstance{
					category: "weak crypto",
					detail:   "Math.random() is not cryptographically secure - use crypto.getRandomValues() or crypto.randomBytes()",
					line:     i + 1,
				})
			}
		}
	}

	return issues
}

// ---- Python detection (text-based) ----

func checkInsecurePatternsPython(filePath, oldContent, newContent string) []string {
	oldIssues := findInsecurePatternsPython(oldContent)
	newIssues := findInsecurePatternsPython(newContent)

	oldSet := make(map[string]bool, len(oldIssues))
	for _, oi := range oldIssues {
		oldSet[oi.category+"|"+oi.detail] = true
	}

	var warnings []string
	reported := 0
	for _, ni := range newIssues {
		key := ni.category + "|" + ni.detail
		if oldSet[key] {
			continue
		}
		oldSet[key] = true
		loc := ""
		if ni.line > 0 {
			loc = fmt.Sprintf(" (line %d)", ni.line)
		}
		warnings = append(warnings, fmt.Sprintf(
			"[Security] %s%s: %s. Review and use a secure alternative.",
			ni.category, loc, ni.detail))
		reported++
		if reported >= 3 {
			break
		}
	}
	return warnings
}

func findInsecurePatternsPython(content string) []insecurePatternInstance {
	var issues []insecurePatternInstance
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Fix #723: strip Python comments and string literals once for ALL
		// checks below (previously only verify=False did this, fix #274).
		// '#' is a comment marker only in Python — Go/JS lines are handled by
		// their own helpers, never here. Comment mentions like
		// `# hashlib.md5(password) is weak` no longer trigger.
		code := pyStripCommentsAndStrings(trimmed)
		lower := strings.ToLower(code)

		// verify=False / verify=0 / ssl=False (requests, httpx, aiohttp,
		// urllib3). Fix #245: previous three-condition substring AND missed
		// httpx/aiohttp calls with no context word and `verify=0` without spaces;
		// the assignment alone is now treated as a strong enough signal.
		// Fix #274: match against comment/string-stripped text so mentions of
		// verify=False inside Python comments (#), docstrings, string literals
		// and URL query strings are not flagged.
		if insecureVerifyDisabledRe.MatchString(code) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "SSL verification disabled (verify=False/ssl=False) - removes MITM protection",
				line:     i + 1,
			})
		}

		// hashlib.md5/sha1 for password hashing
		if (strings.Contains(lower, "hashlib.md5") || strings.Contains(lower, "hashlib.sha1")) &&
			(strings.Contains(lower, "password") || strings.Contains(lower, "token") ||
				strings.Contains(lower, "secret") || strings.Contains(lower, "hash")) {
			issues = append(issues, insecurePatternInstance{
				category: "weak crypto",
				detail:   "MD5/SHA1 for password hashing - use hashlib.sha256+ or bcrypt/passlib",
				line:     i + 1,
			})
		}

		// os.system or subprocess with shell=True and concatenation
		if strings.Contains(lower, "shell=true") &&
			(strings.Contains(trimmed, "+") || strings.Contains(lower, "f\"") ||
				strings.Contains(lower, "format(")) {
			issues = append(issues, insecurePatternInstance{
				category: "command injection",
				detail:   "subprocess with shell=True and formatted input - use shell=False with argument list",
				line:     i + 1,
			})
		}

		// random.random() for security
		if (strings.Contains(lower, "random.random") || strings.Contains(lower, "random.randint")) &&
			isLineSecuritySensitive(lower) {
			issues = append(issues, insecurePatternInstance{
				category: "weak crypto",
				detail:   "random module is not cryptographically secure - use secrets module",
				line:     i + 1,
			})
		}

		// eval()/exec() with dynamic input. Fix #723: the raw-line `#` guard is
		// gone — comment stripping above already removes `# ...` text, and
		// mentions inside string literals are now also skipped.
		if strings.Contains(lower, "eval(") || strings.Contains(lower, "exec(") {
			issues = append(issues, insecurePatternInstance{
				category: "code injection",
				detail:   "eval()/exec() with dynamic content - code injection risk",
				line:     i + 1,
			})
		}
	}

	return issues
}

// ---- Helpers ----

func isSecuritySensitiveName(name string) bool {
	securityWords := []string{"token", "password", "passwd", "secret", "key", "salt",
		"nonce", "session", "auth", "credential", "otp", "captcha", "csrf"}
	for _, w := range securityWords {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

func isLineSecuritySensitive(line string) bool {
	return isSecuritySensitiveName(line)
}

// isSQLKeywordLine checks if a line contains a SQL DML keyword (SELECT/
// INSERT/UPDATE/DELETE) combined with a clause keyword (FROM/INTO/VALUES).
// Fix #245: requiring FROM caused false negatives for INSERT ... VALUES and
// DELETE ... INTO-shaped statements, which have no FROM clause.
func isSQLKeywordLine(upperLine string) bool {
	dml := strings.Contains(upperLine, "SELECT ") || strings.Contains(upperLine, "INSERT ") ||
		strings.Contains(upperLine, "UPDATE ") || strings.Contains(upperLine, "DELETE ")
	if !dml {
		return false
	}
	return strings.Contains(upperLine, "FROM ") || strings.Contains(upperLine, "INTO ") ||
		strings.Contains(upperLine, "VALUES ")
}

// lineHasConcatPlus reports whether the line contains a "+" usable for string
// concatenation. Increments (`i++`) and compound assignments (`+=`) are
// stripped first so they do not count (fix #245: `i++` inside a query loop
// was misreported as SQL injection).
func lineHasConcatPlus(line string) bool {
	s := strings.ReplaceAll(line, "++", "")
	s = strings.ReplaceAll(s, "+=", "")
	return strings.Contains(s, "+")
}

// pyStripCommentsAndStrings removes Python comments and string-literal
// contents from a single line (fix #274; shared by all Python checks since
// fix #723), so mentions of insecure patterns inside comments (`# ...`),
// docstrings, string literals or URL query strings do not trigger the
// detectors. '#' starts a comment when found
// outside a string; '...', "...", ”'...”' and """...""" contents are
// dropped (the delimiting quotes are consumed too).
//
// Simplified (deliberate): strings are handled per line — an unterminated
// string swallows the rest of its line (covers a multi-line docstring's
// opening line) — and backslash-escaped quotes inside strings are not
// interpreted. Acceptable for a line-level heuristic.
func pyStripCommentsAndStrings(line string) string {
	var b strings.Builder
	r := []rune(line)
	n := len(r)
	i := 0
	for i < n {
		c := r[i]
		if c == '#' {
			break // comment: rest of line ignored
		}
		if c == '\'' || c == '"' {
			quote := c
			if i+2 < n && r[i+1] == quote && r[i+2] == quote {
				// Triple-quoted string (docstring): scan for closing triple.
				j := i + 3
				closed := false
				for j+2 < n {
					if r[j] == quote && r[j+1] == quote && r[j+2] == quote {
						i = j + 3
						closed = true
						break
					}
					j++
				}
				if !closed {
					i = n // unterminated: rest of line is string content
				}
			} else {
				// Single-quoted string: scan for the closing quote.
				j := i + 1
				for j < n && r[j] != quote {
					j++
				}
				if j < n {
					i = j + 1
				} else {
					i = n // unterminated: swallow rest of line
				}
			}
			continue
		}
		b.WriteRune(c)
		i++
	}
	return b.String()
}

// cStyleBlockCommentLine applies /* */ block-comment state tracking to one
// trimmed C-style (Go/JS) code line (fix #728). It returns the code portion
// of the line and ok=false when the entire line is comment and must be
// skipped. `inBlock` carries the cross-line block-comment state.
//
// Semantics:
//   - While inside a block, a line without `*/` is entirely comment; a line
//     containing `*/` yields the code after the closer.
//   - A line starting with `//` is a full-line comment (fix #723 behavior).
//   - A line starting with `/*` is skipped entirely (fix #723 behavior); if it
//     does not close on the same line, the block state opens.
//   - A line where `/*` opens mid-line and does not close keeps the code
//     before the opener and opens the block state (mixed-line handling).
func cStyleBlockCommentLine(trimmed string, inBlock *bool) (string, bool) {
	if *inBlock {
		end := strings.Index(trimmed, "*/")
		if end < 0 {
			return "", false // still inside block comment body
		}
		*inBlock = false
		trimmed = strings.TrimSpace(trimmed[end+2:])
		if trimmed == "" {
			return "", false
		}
	}
	if strings.HasPrefix(trimmed, "//") {
		return "", false
	}
	if strings.HasPrefix(trimmed, "/*") {
		if !strings.Contains(trimmed[2:], "*/") {
			*inBlock = true
		}
		return "", false
	}
	if idx := strings.Index(trimmed, "/*"); idx >= 0 && !strings.Contains(trimmed[idx+2:], "*/") {
		*inBlock = true
		trimmed = strings.TrimSpace(trimmed[:idx])
		if trimmed == "" {
			return "", false
		}
	}
	return trimmed, true
}

// goStripTrailingComment removes a trailing // or /* comment from a single
// C-style code line (Go and JS; fix #278, shared by all checks since fix
// #723), so insecure patterns inside trailing comments do not trigger the
// text-based detectors.
//
// Simplified (deliberate): only the spaced forms " // " and " /* " are treated
// as comment starts, which keeps URL literals like "http://example.com"
// (no spaces around the slashes) intact without full string-aware scanning.
// Unspaced trailing comments (`x := 1// c`) and /* inside string literals are
// not handled — a conservative false-negative tradeoff for a line-level
// heuristic.
func goStripTrailingComment(line string) string {
	if idx := strings.Index(line, " // "); idx >= 0 {
		line = line[:idx]
	}
	if idx := strings.Index(line, " /* "); idx >= 0 {
		line = line[:idx]
	}
	return strings.TrimSpace(line)
}
