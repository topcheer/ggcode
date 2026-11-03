package agent

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"regexp"
	"strings"
)

var insecureVerifyDisabledRe = regexp.MustCompile(`(?i)\b(?:ssl_verify|verify|ssl)\s*=\s*(?:false|0)\b`)

var insecureRejectUnauthorizedRe = regexp.MustCompile(`(?i)rejectUnauthorized\s*:\s*(?:false|0)\b`)

var insecureNodeTLSDisabledRe = regexp.MustCompile(`(?i)NODE_TLS_REJECT_UNAUTHORIZED\s*=\s*(?:(?:['"]?(?:0|false)['"]?)|;?\s*$)`)

var insecureSkipVerifyTrueRe = regexp.MustCompile(`InsecureSkipVerify\s*[:=]\s*true\b`)

type insecurePatternInstance struct {
	category string
	detail   string
	line     int
}

func checkInsecurePatterns(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := filepath.Ext(filePath)
	switch ext {
	case ".go":
		return checkInsecurePatternsGo(filePath, oldContent, newContent)
	case ".js", ".ts", ".jsx", ".tsx", ".mjs", ".mts", ".cjs", ".cts":
		return checkInsecurePatternsJS(filePath, oldContent, newContent)
	case ".py", ".pyw":
		return checkInsecurePatternsPython(filePath, oldContent, newContent)
	default:
		return nil
	}
}

func checkInsecurePatternsGo(filePath, oldContent, newContent string) []string {
	oldIssues := findInsecurePatternsGo(oldContent)
	newIssues := findInsecurePatternsGo(newContent)

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

func findInsecurePatternsGo(content string) []insecurePatternInstance {
	var issues []insecurePatternInstance

	lines := strings.Split(content, "\n")
	inBlockComment := false
	inRawString := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		code, ok := cStyleBlockCommentLine(trimmed, &inBlockComment)
		if !ok {
			continue
		}

		signalLine := code
		code = goStripStringLiterals(code, &inRawString)
		trimmed = goStripTrailingComment(code)

		if insecureSkipVerifyTrueRe.MatchString(trimmed) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "InsecureSkipVerify: true disables TLS certificate verification",
				line:     i + 1,
			})
		}

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

		upperLine := strings.ToUpper(goStripTrailingComment(signalLine))
		if isSQLKeywordLine(upperLine) {

			if lineHasConcatPlus(trimmed) || strings.Contains(trimmed, "Sprintf") {
				issues = append(issues, insecurePatternInstance{
					category: "SQL injection",
					detail:   "SQL query built with string concatenation/Sprintf - use parameterized queries (? placeholders)",
					line:     i + 1,
				})
			}
		}

		cmdSignal := goStripTrailingComment(signalLine)
		if strings.Contains(trimmed, "exec.Command") {
			if strings.Contains(cmdSignal, "\"sh\"") || strings.Contains(cmdSignal, "\"bash\"") ||
				strings.Contains(cmdSignal, "\"/bin/sh\"") || strings.Contains(cmdSignal, "\"/bin/bash\"") {
				if lineHasConcatPlus(trimmed) || strings.Contains(trimmed, "Sprintf") {
					issues = append(issues, insecurePatternInstance{
						category: "command injection",
						detail:   "exec.Command with shell and concatenated input - pass arguments as separate args",
						line:     i + 1,
					})
				}
			}
		}
	}

	tree, fset, err := parseGoSource("", content, 0)
	if err == nil {

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

					continue
				}

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
	inBlockComment := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		code, ok := cStyleBlockCommentLine(trimmed, &inBlockComment)
		if !ok {
			continue
		}
		trimmed = goStripTrailingComment(code)

		trimmed = jsStripCommentsAndStrings(trimmed)

		lower := strings.ToLower(trimmed)

		if insecureRejectUnauthorizedRe.MatchString(trimmed) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "rejectUnauthorized: false disables TLS certificate verification",
				line:     i + 1,
			})
		}

		if insecureNodeTLSDisabledRe.MatchString(trimmed) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "NODE_TLS_REJECT_UNAUTHORIZED=0 disables all TLS verification globally",
				line:     i + 1,
			})
		}

		if strings.Contains(lower, "eval(") && !strings.Contains(lower, "evaluate") {
			issues = append(issues, insecurePatternInstance{
				category: "code injection",
				detail:   "eval() executes arbitrary code - avoid or use JSON.parse for data",
				line:     i + 1,
			})
		}

		if strings.Contains(lower, "innerhtml") && (strings.Contains(trimmed, "+") ||
			strings.Contains(lower, "template") || strings.Contains(lower, "${")) {
			issues = append(issues, insecurePatternInstance{
				category: "XSS",
				detail:   "innerHTML with dynamic content - XSS risk, use textContent or sanitize",
				line:     i + 1,
			})
		}

		if strings.Contains(lower, "math.random()") {

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

		code := pyStripCommentsAndStrings(trimmed)
		lower := strings.ToLower(code)

		if insecureVerifyDisabledRe.MatchString(code) {
			issues = append(issues, insecurePatternInstance{
				category: "TLS bypass",
				detail:   "SSL verification disabled (verify=False/ssl=False) - removes MITM protection",
				line:     i + 1,
			})
		}

		if (strings.Contains(lower, "hashlib.md5") || strings.Contains(lower, "hashlib.sha1")) &&
			(strings.Contains(lower, "password") || strings.Contains(lower, "token") ||
				strings.Contains(lower, "secret")) {
			issues = append(issues, insecurePatternInstance{
				category: "weak crypto",
				detail:   "MD5/SHA1 for password hashing - use hashlib.sha256+ or bcrypt/passlib",
				line:     i + 1,
			})
		}

		if strings.Contains(lower, "shell=true") &&
			(lineHasConcatPlus(code) || strings.Contains(lower, "f\"\"") ||
				strings.Contains(lower, "format(")) {
			issues = append(issues, insecurePatternInstance{
				category: "command injection",
				detail:   "subprocess with shell=True and formatted input - use shell=False with argument list",
				line:     i + 1,
			})
		}

		if (strings.Contains(lower, "random.random") || strings.Contains(lower, "random.randint")) &&
			isLineSecuritySensitive(lower) {
			issues = append(issues, insecurePatternInstance{
				category: "weak crypto",
				detail:   "random module is not cryptographically secure - use secrets module",
				line:     i + 1,
			})
		}

		hasEvalCall := strings.Contains(lower, "eval(") &&
			!strings.Contains(lower, "literal_eval") && !strings.Contains(lower, ".eval(")
		if hasEvalCall || strings.Contains(lower, "exec(") {
			issues = append(issues, insecurePatternInstance{
				category: "code injection",
				detail:   "eval()/exec() with dynamic content - code injection risk",
				line:     i + 1,
			})
		}
	}

	return issues
}

func isSecuritySensitiveName(name string) bool {

	hasKeyBoundary := strings.Contains(name, "Key") ||
		strings.Contains(strings.ToLower(name), "_key") ||
		strings.EqualFold(name, "key")
	securityWords := []string{"token", "password", "passwd", "secret", "salt",
		"nonce", "session", "auth", "credential", "otp", "captcha", "csrf"}
	if hasKeyBoundary {
		return true
	}
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

func isSQLKeywordLine(upperLine string) bool {
	dml := strings.Contains(upperLine, "SELECT ") || strings.Contains(upperLine, "INSERT ") ||
		strings.Contains(upperLine, "UPDATE ") || strings.Contains(upperLine, "DELETE ")
	if !dml {
		return false
	}
	return strings.Contains(upperLine, "FROM ") || strings.Contains(upperLine, "INTO ") ||
		strings.Contains(upperLine, "VALUES ")
}

func lineHasConcatPlus(line string) bool {
	s := strings.ReplaceAll(line, "++", "")
	s = strings.ReplaceAll(s, "+=", "")
	return strings.Contains(s, "+")
}

func pyStripCommentsAndStrings(line string) string {
	var b strings.Builder
	r := []rune(line)
	n := len(r)
	i := 0
	var prev rune
	for i < n {
		c := r[i]
		if c == '#' {
			break
		}
		if c == '\'' || c == '"' {
			quote := c
			if i+2 < n && r[i+1] == quote && r[i+2] == quote {

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
					i = n
				}
			} else {

				j := i + 1
				for j < n && r[j] != quote {
					j++
				}
				if j < n {
					i = j + 1
				} else {
					i = n
				}
			}

			if prev == 'f' || prev == 'F' {
				b.WriteString(`""`)
			}
			continue
		}
		prev = c
		b.WriteRune(c)
		i++
	}
	return b.String()
}

func cStyleBlockCommentLine(trimmed string, inBlock *bool) (string, bool) {
	if *inBlock {
		end := strings.Index(trimmed, "*/")
		if end < 0 {
			return "", false
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
	if idx := strings.Index(trimmed, "/*"); idx >= 0 {
		if end := strings.Index(trimmed[idx+2:], "*/"); end >= 0 {

			trimmed = strings.TrimSpace(trimmed[:idx] + trimmed[idx+2+end+2:])
			if trimmed == "" {
				return "", false
			}
		} else {
			*inBlock = true
			trimmed = strings.TrimSpace(trimmed[:idx])
			if trimmed == "" {
				return "", false
			}
		}
	}
	return trimmed, true
}

func jsStripCommentsAndStrings(line string) string {
	var b strings.Builder
	r := []rune(line)
	n := len(r)
	i := 0
	for i < n {
		c := r[i]
		if c == '/' && i+1 < n && r[i+1] == '/' {
			break
		}
		if c == '\'' || c == '"' {
			quote := c
			j := i + 1
			for j < n && r[j] != quote {
				if r[j] == '\\' && j+1 < n {
					j++
				}
				j++
			}
			i = j + 1
			continue
		}
		if c == '`' {

			i++
			for i < n && r[i] != '`' {
				if r[i] == '$' && i+1 < n && r[i+1] == '{' {
					b.WriteString("${")
					i += 2
					depth := 1
					for i < n && depth > 0 {
						if r[i] == '{' {
							depth++
						} else if r[i] == '}' {
							depth--
							if depth == 0 {
								i++
								break
							}
						}
						b.WriteRune(r[i])
						i++
					}
					continue
				}
				i++
			}
			i++
			continue
		}
		b.WriteRune(c)
		i++
	}
	return b.String()
}

func goStripStringLiterals(line string, inRaw *bool) string {
	var b strings.Builder
	r := []rune(line)
	n := len(r)
	i := 0
	for i < n {
		c := r[i]
		if *inRaw {

			for i < n && r[i] != '`' {
				i++
			}
			if i < n {
				b.WriteRune('`')
				i++
				*inRaw = false
			}
			continue
		}
		switch {
		case c == '"':
			b.WriteRune('"')
			i++
			for i < n && r[i] != '"' {
				if r[i] == '\\' && i+1 < n {
					i++
				}
				i++
			}
			if i < n {
				b.WriteRune('"')
				i++
			}
		case c == '`':
			b.WriteRune('`')
			i++
			for i < n && r[i] != '`' {
				i++
			}
			if i < n {
				b.WriteRune('`')
				i++
			} else {
				*inRaw = true
			}
		case c == '\'':
			b.WriteRune('\'')
			i++
			for i < n && r[i] != '\'' {
				if r[i] == '\\' && i+1 < n {
					i++
				}
				i++
			}
			if i < n {
				b.WriteRune('\'')
				i++
			}
		default:
			b.WriteRune(c)
			i++
		}
	}
	return b.String()
}

func goStripTrailingComment(line string) string {
	if idx := strings.Index(line, " // "); idx >= 0 {
		line = line[:idx]
	}
	if idx := strings.Index(line, " /* "); idx >= 0 {
		line = line[:idx]
	}
	return strings.TrimSpace(line)
}
