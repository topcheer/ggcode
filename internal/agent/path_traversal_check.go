package agent

// Path Traversal Vulnerability Detection in File Writes
//
// Research basis: OWASP A01:2021 Broken Access Control. Path traversal is
// the #1 web vulnerability category. AI coding agents frequently generate
// file-access code that trusts user-controlled input without sanitization:
//
//	vulnerable := filepath.Join(basePath, r.URL.Query().Get("file"))
//	data, _ := os.ReadFile(dir + "/" + userInput)
//	http.ServeFile(w, r, req.FormValue("path"))
//
// An attacker can supply "../../../etc/passwd" to escape the intended
// directory. The fix is to validate/clean the path:
//
//	cleaned := filepath.Clean(userInput)
//	if strings.HasPrefix(cleaned, "..") { return error }
//
// Competitor analysis:
//   - Claude Code: no write-time path traversal detection
//   - Cursor: relies on external linters (gosec G304, eslint-plugin-security)
//   - Cline/OpenHands: no detection
//   - GitHub Copilot: no detection in suggestions
//   - Aider: no detection
//   - gosec: detects G304 (file inclusion from variable) but only at CI time
//
// ggcode's approach: lightweight pattern matching that runs at write time.
// Delta-aware: only flags patterns INTRODUCED by this edit. Zero LLM cost.

import (
	"fmt"
	"strings"
)

// pathTraversalInstance represents one detected traversal-risk pattern.
type pathTraversalInstance struct {
	category string // "path traversal", "explicit traversal"
	detail   string
	line     int
}

// checkPathTraversal detects path traversal vulnerabilities introduced by
// this edit. Multi-language: Go, JS/TS, Python. Returns warning strings.
func checkPathTraversal(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	var newIssues, oldIssues []pathTraversalInstance
	switch detectLangExt(filePath) {
	case "go":
		newIssues = findPathTraversalGo(newContent)
		oldIssues = findPathTraversalGo(oldContent)
	case "js":
		newIssues = findPathTraversalJS(newContent)
		oldIssues = findPathTraversalJS(oldContent)
	case "py":
		newIssues = findPathTraversalPython(newContent)
		oldIssues = findPathTraversalPython(oldContent)
	default:
		return nil
	}
	return ptDeltaWarnings(oldIssues, newIssues)
}

// ptDeltaWarnings returns only genuinely-new traversal instances.
func ptDeltaWarnings(oldIssues, newIssues []pathTraversalInstance) []string {
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
			"[Security] %s%s: %s. Sanitize input with filepath.Clean and reject '..' sequences.",
			ni.category, loc, ni.detail))
		reported++
		if reported >= 3 {
			break
		}
	}
	return warnings
}

// ---- Go detection ----

// ptGoFileFuncs are functions that read/serve files and are vulnerable
// when called with user-controlled paths.
var ptGoFileFuncs = []string{
	"os.Open", "os.ReadFile", "os.OpenFile", "os.Stat",
	"ioutil.ReadFile", "filepath.Join",
	"http.ServeFile", "http.Dir", "http.ServeFileFS",
}

// ptGoUserInput indicators on the same line suggest user-controlled data.
var ptGoUserInput = []string{
	"Query(", "Param(", "r.URL", "FormValue", "PostFormValue",
	"Header.Get", "r.Form", ".Path", "Body", "Request.URL",
}

// findPathTraversalGo scans for path traversal risk patterns in Go code.
func findPathTraversalGo(content string) []pathTraversalInstance {
	var issues []pathTraversalInstance
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		issue := ptScanGoLine(line)
		if issue != nil {
			issue.line = i + 1
			issues = append(issues, *issue)
		}
	}
	return issues
}

// ptScanGoLine checks a single Go line for traversal risk.
func ptScanGoLine(line string) *pathTraversalInstance {
	trimmed := strings.TrimSpace(line)

	// 1. Explicit ".." in path string literal -- strong signal.
	if ptContainsTraversalLiteral(trimmed) {
		return &pathTraversalInstance{
			category: "explicit traversal",
			detail:   "literal '..' in path construction allows directory escape",
		}
	}

	// 2. File I/O function + user input indicator + concatenation.
	if ptHasAny(trimmed, ptGoFileFuncs) && ptHasAny(trimmed, ptGoUserInput) {
		if strings.Contains(trimmed, "+") || strings.Contains(trimmed, "Sprintf") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "file operation with concatenated user input -- sanitize with filepath.Clean and validate",
			}
		}
		// filepath.Join with user input even without explicit concat.
		if strings.Contains(trimmed, "filepath.Join") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "filepath.Join with user input -- verify no '..' can escape the base directory",
			}
		}
	}

	// 3. http.ServeFile with a variable (non-literal) path.
	if strings.Contains(trimmed, "http.ServeFile") && !ptAllStringLiteralArg(trimmed) {
		return &pathTraversalInstance{
			category: "path traversal",
			detail:   "http.ServeFile with dynamic path -- ensure the path is cleaned and validated",
		}
	}
	return nil
}

// ---- JavaScript/TypeScript detection ----

var ptJSFileFuncs = []string{
	"readFile", "readFileSync", "createReadStream",
	"writeFile", "readdir", "stat", "access",
}

var ptJSPathFuncs = []string{
	"path.join", "path.resolve", "path.normalize",
}

var ptJSUserInput = []string{
	"req.params", "req.query", "req.body", "req.headers",
	"request.params", "request.query", "request.body",
}

// findPathTraversalJS scans for path traversal risk in JS/TS code.
func findPathTraversalJS(content string) []pathTraversalInstance {
	var issues []pathTraversalInstance
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		issue := ptScanJSLine(line)
		if issue != nil {
			issue.line = i + 1
			issues = append(issues, *issue)
		}
	}
	return issues
}

// ptScanJSLine checks a single JS/TS line for traversal risk.
func ptScanJSLine(line string) *pathTraversalInstance {
	trimmed := strings.TrimSpace(line)

	// Explicit ".." literal.
	if ptContainsTraversalLiteral(trimmed) {
		return &pathTraversalInstance{
			category: "explicit traversal",
			detail:   "literal '..' in path construction allows directory escape",
		}
	}

	// path.join/resolve + user input.
	if ptHasAny(trimmed, ptJSPathFuncs) && ptHasAny(trimmed, ptJSUserInput) {
		return &pathTraversalInstance{
			category: "path traversal",
			detail:   "path.join/resolve with user input -- sanitize and reject '..' sequences",
		}
	}

	// fs operation + concatenation + user input.
	if ptHasAny(trimmed, ptJSFileFuncs) && ptHasAny(trimmed, ptJSUserInput) {
		if strings.Contains(trimmed, "+") || strings.Contains(trimmed, "${") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "file operation with concatenated user input -- sanitize path first",
			}
		}
	}
	return nil
}

// ---- Python detection ----

var ptPyUserInput = []string{
	"request.args", "request.form", "request.json",
	"request.data", "request.values", "request.files",
}

// findPathTraversalPython scans for path traversal risk in Python code.
func findPathTraversalPython(content string) []pathTraversalInstance {
	var issues []pathTraversalInstance
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		issue := ptScanPythonLine(line)
		if issue != nil {
			issue.line = i + 1
			issues = append(issues, *issue)
		}
	}
	return issues
}

// ptScanPythonLine checks a single Python line for traversal risk.
func ptScanPythonLine(line string) *pathTraversalInstance {
	trimmed := strings.TrimSpace(line)

	// Explicit ".." literal.
	if ptContainsTraversalLiteral(trimmed) {
		return &pathTraversalInstance{
			category: "explicit traversal",
			detail:   "literal '..' in path construction allows directory escape",
		}
	}

	// open() / os.path.join / send_file with user input + concatenation.
	hasFileOp := strings.Contains(trimmed, "open(") ||
		strings.Contains(trimmed, "os.path.join") ||
		strings.Contains(trimmed, "send_file") ||
		strings.Contains(trimmed, "send_from_directory")
	if hasFileOp && ptHasAny(trimmed, ptPyUserInput) {
		if strings.Contains(trimmed, "+") || strings.Contains(trimmed, "f\"") ||
			strings.Contains(trimmed, "format(") || strings.Contains(trimmed, "%s") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "file operation with concatenated user input -- use werkzeug secure_filename or validate path",
			}
		}
		// os.path.join with user input even without explicit concatenation.
		if strings.Contains(trimmed, "os.path.join") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "os.path.join with user input -- sanitize and validate the resulting path",
			}
		}
		// send_file / send_from_directory with user input (no concat needed --
		// these directly serve user-controlled paths).
		if strings.Contains(trimmed, "send_file") || strings.Contains(trimmed, "send_from_directory") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "send_file with user-controlled path -- validate or use safe_send_from_directory",
			}
		}
	}
	return nil
}

// ---- Shared helpers ----

// ptContainsTraversalLiteral checks for explicit ".." in string concatenation.
func ptContainsTraversalLiteral(line string) bool {
	return strings.Contains(line, "\"../") || strings.Contains(line, "'../") ||
		strings.Contains(line, "\"..\\\\") || strings.Contains(line, "`../") ||
		strings.Contains(line, "/..\"") || strings.Contains(line, "/..'")
}

// ptHasAny returns true if the line contains any of the given substrings.
func ptHasAny(line string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(line, p) {
			return true
		}
	}
	return false
}

// ptAllStringLiteralArg is a heuristic: if the ServeFile line's path argument
// appears to be a string literal (quoted), it's less risky. Returns true if
// we believe the argument is a static string literal.
func ptAllStringLiteralArg(line string) bool {
	// Look for pattern like http.ServeFile(w, r, "static/file.html")
	idx := strings.Index(line, "ServeFile")
	if idx < 0 {
		return false
	}
	rest := line[idx:]
	parenStart := strings.Index(rest, "(")
	if parenStart < 0 {
		return false
	}
	rest = rest[parenStart:]
	// Find the last argument (3rd arg typically).
	// If it starts with a quote, treat as literal.
	parts := strings.Split(rest, ",")
	if len(parts) < 3 {
		return false
	}
	lastArg := strings.TrimSpace(parts[len(parts)-1])
	return strings.HasPrefix(lastArg, "\"") || strings.HasPrefix(lastArg, "'") ||
		strings.HasPrefix(lastArg, "`")
}

// detectLangExt returns a normalized language code from the file extension.
func detectLangExt(filePath string) string {
	switch {
	case strings.HasSuffix(filePath, ".go"):
		return "go"
	case strings.HasSuffix(filePath, ".js"), strings.HasSuffix(filePath, ".jsx"),
		strings.HasSuffix(filePath, ".ts"), strings.HasSuffix(filePath, ".tsx"),
		strings.HasSuffix(filePath, ".mjs"):
		return "js"
	case strings.HasSuffix(filePath, ".py"):
		return "py"
	default:
		return ""
	}
}
