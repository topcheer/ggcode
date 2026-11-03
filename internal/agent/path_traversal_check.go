package agent

import (
	"fmt"
	"regexp"
	"strings"
)

type pathTraversalInstance struct {
	category string
	detail   string
	line     int
	lineText string
}

func (i pathTraversalInstance) ptFingerprint() string {
	return i.category + "|" + i.lineText
}

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

func ptDeltaWarnings(oldIssues, newIssues []pathTraversalInstance) []string {
	oldSet := make(map[string]bool, len(oldIssues))
	for _, oi := range oldIssues {
		oldSet[oi.ptFingerprint()] = true
	}
	var warnings []string
	reported := 0
	for _, ni := range newIssues {
		key := ni.ptFingerprint()
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

var ptGoFileFuncs = []string{
	"os.Open", "os.ReadFile", "os.OpenFile", "os.Stat",
	"ioutil.ReadFile", "filepath.Join",
	"http.ServeFile", "http.Dir", "http.ServeFileFS",
}

var ptGoUserInput = []string{
	"Query(", "Param(", "r.URL", "FormValue", "PostFormValue",
	"Header.Get", "r.Form", ".Path", "Body", "Request.URL",
}

func findPathTraversalGo(content string) []pathTraversalInstance {
	var issues []pathTraversalInstance
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		issue := ptScanGoLine(line)
		if issue != nil {
			issue.line = i + 1
			issue.lineText = strings.TrimSpace(line)
			issues = append(issues, *issue)
		}
	}
	return issues
}

func ptScanGoLine(line string) *pathTraversalInstance {
	trimmed := strings.TrimSpace(line)

	if ptContainsTraversalLiteral(trimmed) {
		return &pathTraversalInstance{
			category: "explicit traversal",
			detail:   "literal '..' in path construction allows directory escape",
		}
	}

	if ptHasAny(trimmed, ptGoFileFuncs) && ptHasAny(trimmed, ptGoUserInput) {
		if strings.Contains(trimmed, "+") || strings.Contains(trimmed, "Sprintf") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "file operation with concatenated user input -- sanitize with filepath.Clean and validate",
			}
		}

		if strings.Contains(trimmed, "filepath.Join") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "filepath.Join with user input -- verify no '..' can escape the base directory",
			}
		}
	}

	if strings.Contains(trimmed, "http.ServeFile") && !ptAllStringLiteralArg(trimmed) {
		return &pathTraversalInstance{
			category: "path traversal",
			detail:   "http.ServeFile with dynamic path -- ensure the path is cleaned and validated",
		}
	}
	return nil
}

var ptJSFileFuncs = []string{
	"readFile", "readFileSync", "createReadStream",
	"writeFile", "readdir", "stat", "access",
}

var ptJSFileFuncRe = regexp.MustCompile(
	`(?i)\b(` + strings.Join(ptJSFileFuncs, "|") + `)\s*\(`)

var ptJSPathFuncs = []string{
	"path.join", "path.resolve", "path.normalize",
}

var ptJSUserInput = []string{
	"req.params", "req.query", "req.body", "req.headers",
	"request.params", "request.query", "request.body",
}

func findPathTraversalJS(content string) []pathTraversalInstance {
	var issues []pathTraversalInstance
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		issue := ptScanJSLine(line)
		if issue != nil {
			issue.line = i + 1
			issue.lineText = strings.TrimSpace(line)
			issues = append(issues, *issue)
		}
	}
	return issues
}

func ptScanJSLine(line string) *pathTraversalInstance {
	trimmed := strings.TrimSpace(line)

	if ptContainsTraversalLiteral(trimmed) {
		return &pathTraversalInstance{
			category: "explicit traversal",
			detail:   "literal '..' in path construction allows directory escape",
		}
	}

	if ptHasAny(trimmed, ptJSPathFuncs) && ptHasAny(trimmed, ptJSUserInput) {
		return &pathTraversalInstance{
			category: "path traversal",
			detail:   "path.join/resolve with user input -- sanitize and reject '..' sequences",
		}
	}

	if ptJSFileFuncRe.MatchString(trimmed) && ptHasAny(trimmed, ptJSUserInput) {
		if strings.Contains(trimmed, "+") || strings.Contains(trimmed, "${") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "file operation with concatenated user input -- sanitize path first",
			}
		}
	}
	return nil
}

var ptPyUserInput = []string{
	"request.args", "request.form", "request.json",
	"request.data", "request.values", "request.files",
}

func findPathTraversalPython(content string) []pathTraversalInstance {
	var issues []pathTraversalInstance
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		issue := ptScanPythonLine(line)
		if issue != nil {
			issue.line = i + 1
			issue.lineText = strings.TrimSpace(line)
			issues = append(issues, *issue)
		}
	}
	return issues
}

func ptScanPythonLine(line string) *pathTraversalInstance {
	trimmed := strings.TrimSpace(line)

	if ptContainsTraversalLiteral(trimmed) {
		return &pathTraversalInstance{
			category: "explicit traversal",
			detail:   "literal '..' in path construction allows directory escape",
		}
	}

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

		if strings.Contains(trimmed, "os.path.join") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "os.path.join with user input -- sanitize and validate the resulting path",
			}
		}

		if strings.Contains(trimmed, "send_file") || strings.Contains(trimmed, "send_from_directory") {
			return &pathTraversalInstance{
				category: "path traversal",
				detail:   "send_file with user-controlled path -- validate or use safe_send_from_directory",
			}
		}
	}
	return nil
}

func ptContainsTraversalLiteral(line string) bool {
	return strings.Contains(line, "\"../") || strings.Contains(line, "'../") ||
		strings.Contains(line, "\"..\\\\") || strings.Contains(line, "`../") ||
		strings.Contains(line, "/..\"") || strings.Contains(line, "/..'")
}

func ptHasAny(line string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(line, p) {
			return true
		}
	}
	return false
}

func ptAllStringLiteralArg(line string) bool {

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

	parts := strings.Split(rest, ",")
	if len(parts) < 3 {
		return false
	}
	lastArg := strings.TrimSpace(parts[len(parts)-1])
	return strings.HasPrefix(lastArg, "\"") || strings.HasPrefix(lastArg, "'") ||
		strings.HasPrefix(lastArg, "`")
}

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
