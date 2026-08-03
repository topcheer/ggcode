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
	"strings"
)

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

	// Delta: count how many new issues exist beyond what was already there.
	newCount := len(newIssues) - len(oldIssues)
	if newCount <= 0 {
		return nil
	}

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
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

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

		// 3. SQL injection via string concatenation: "SELECT" + variable or fmt.Sprintf with SELECT
		upperLine := strings.ToUpper(trimmed)
		if isSQLKeywordLine(upperLine) {
			// Check for concatenation or Sprintf in the same line
			if strings.Contains(trimmed, "+") || strings.Contains(trimmed, "Sprintf") ||
				strings.Contains(trimmed, "fmt.Sprintf") {
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
		ast.Inspect(tree, func(n ast.Node) bool {
			// Detect math/rand.Read or rand.Intn assigned to token/key/secret/password variables.
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, expr := range assign.Rhs {
				call, ok := expr.(*ast.CallExpr)
				if !ok {
					continue
				}
				fnName := exprToString(call.Fun)
				// math/rand.Read, rand.Read, rand.Intn, rand.Int63, rand.Float64
				isMathRand := false
				if strings.Contains(fnName, "rand.Read") || strings.Contains(fnName, "rand.Intn") ||
					strings.Contains(fnName, "rand.Int63") || strings.Contains(fnName, "rand.Float64") ||
					strings.Contains(fnName, "rand.Int31") {
					// Exclude crypto/rand (handled by package prefix check below)
					if !strings.Contains(fnName, "crypto") {
						isMathRand = true
					}
				}
				if !isMathRand {
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

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		// rejectUnauthorized: false (Node.js TLS bypass)
		if strings.Contains(lower, "rejectunauthorized") &&
			(strings.Contains(lower, "false") || strings.Contains(lower, "0")) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "rejectUnauthorized: false disables TLS certificate verification",
				line:     i + 1,
			})
		}

		// process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0'
		if strings.Contains(lower, "node_tls_reject_unauthorized") &&
			(strings.Contains(lower, "'0'") || strings.Contains(lower, "\"0\"") ||
				strings.Contains(lower, "false")) {
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
		lower := strings.ToLower(trimmed)

		// verify=False in requests
		if strings.Contains(lower, "verify") &&
			(strings.Contains(lower, "false") || strings.Contains(lower, "= 0")) &&
			(strings.Contains(lower, "request") || strings.Contains(lower, "ssl") ||
				strings.Contains(lower, "session")) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "SSL verification disabled (verify=False) - removes MITM protection",
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

		// eval()/exec() with dynamic input
		if (strings.Contains(lower, "eval(") || strings.Contains(lower, "exec(")) &&
			!strings.Contains(lower, "#") {
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

// isSQLKeywordLine checks if a line contains SQL DML keywords combined with FROM.
func isSQLKeywordLine(upperLine string) bool {
	return (strings.Contains(upperLine, "SELECT ") || strings.Contains(upperLine, "INSERT ") ||
		strings.Contains(upperLine, "UPDATE ") || strings.Contains(upperLine, "DELETE ")) &&
		strings.Contains(upperLine, "FROM ")
}
