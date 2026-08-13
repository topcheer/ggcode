package agent

// Hardcoded Host/Port Detection (sa-71)
//
// Problem: AI coding agents frequently emit server/listener code with
// hardcoded bind addresses like ":8080", "localhost:3000", or "0.0.0.0:5000"
// instead of reading from environment variables or configuration. This
// causes:
//   - Deployment inflexibility: port conflicts in container orchestration
//   - Security exposure: binding to 0.0.0.0 without intent exposes services
//   - Testing friction: parallel tests collide on the same port
//   - 12-factor violation: config should come from the environment
//
// Competitor analysis:
//   - Claude Code: no inline detection; relies on review comments
//   - Cursor: no inline detection; lint-on-save may catch via external rules
//   - Cline/OpenHands: reactive only
//   - Aider: no detection
//   - Windsurf: no detection
//   - gosec (G304): covers file path injection, not bind addresses
//
// None provide write-time inline detection. This check uses Go AST for Go
// files and regex for JS/TS/Python, running in <1ms per file with zero LLM
// cost.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// hardcodedHostMaxWarnings caps the number of warnings to avoid flooding.
const hardcodedHostMaxWarnings = 4

// wellKnownPorts that are commonly hardcoded in development. We don't flag
// standard protocol ports (80, 443) since those are intentional in production.
var devPorts = map[string]bool{
	"3000": true, "3001": true, "4000": true, "4200": true,
	"5000": true, "5173": true, "5432": true,
	"7000": true, "7070": true, "8000": true, "8080": true, "8081": true,
	"8443": true, "8888": true, "9000": true, "9090": true, "9091": true,
	"6379": true, "27017": true,
}

// checkHardcodedHost detects hardcoded bind addresses and ports in server
// code across Go, JS/TS, and Python. It returns warnings for newly
// introduced patterns only (delta-aware).
func checkHardcodedHost(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	var findings []string

	switch ext {
	case ".go":
		findings = findGoHardcodedHost(filePath, newContent)
	case ".js", ".jsx", ".ts", ".tsx", ".mjs":
		findings = findJSTSHardcodedHost(newContent)
	case ".py":
		findings = findPythonHardcodedHost(newContent)
	default:
		return nil
	}

	if len(findings) == 0 {
		return nil
	}

	// Delta-aware: subtract patterns present in old content.
	if strings.TrimSpace(oldContent) != "" {
		oldSet := make(map[string]bool)
		for _, f := range findGoHardcodedHost(filePath, oldContent) {
			oldSet[f] = true
		}
		for _, f := range findJSTSHardcodedHost(oldContent) {
			oldSet[f] = true
		}
		for _, f := range findPythonHardcodedHost(oldContent) {
			oldSet[f] = true
		}
		filtered := findings[:0]
		for _, f := range findings {
			if !oldSet[f] {
				filtered = append(filtered, f)
			}
		}
		findings = filtered
	}

	if len(findings) > hardcodedHostMaxWarnings {
		findings = append(findings[:hardcodedHostMaxWarnings],
			fmt.Sprintf("...and %d more hardcoded host/port pattern(s)", len(findings)-hardcodedHostMaxWarnings))
	}
	return findings
}

// --- Go AST-based detection ---

// goListenFuncs maps Go functions/methods that take a bind address as
// their first string argument.
var goListenFuncs = map[string]bool{
	"ListenAndServe":    true,
	"ListenAndServeTLS": true,
	"Serve":             false, // second arg is listener, skip
	"Listen":            true,
}

// findGoHardcodedHost uses Go AST to detect hardcoded addresses in
// ListenAndServe, net.Listen, and similar calls.
func findGoHardcodedHost(filePath, content string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, 0)
	if err != nil || file == nil {
		return nil
	}

	var findings []string

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		if msg := matchGoListenCall(fset, call); msg != "" {
			findings = append(findings, msg)
			return true
		}

		return true
	})

	return findings
}

// matchGoListenCall checks if a CallExpr is a known listen function with a
// hardcoded address literal. Returns a warning string or "".
func matchGoListenCall(fset *token.FileSet, call *ast.CallExpr) string {
	fnName := extractCallName(call)
	if fnName == "" {
		return ""
	}

	// Check known listen patterns.
	addrIdx := -1
	switch {
	case fnName == "http.ListenAndServe" || fnName == "http.ListenAndServeTLS":
		addrIdx = 0
	case fnName == "net.Listen":
		// net.Listen("tcp", ":8080") -- addr is arg[1]
		if len(call.Args) < 2 {
			return ""
		}
		addrIdx = 1
	case strings.HasSuffix(fnName, ".ListenAndServe") || strings.HasSuffix(fnName, ".ListenAndServeTLS"):
		addrIdx = 0
	case fnName == "ListenAndServe" || fnName == "ListenAndServeTLS":
		addrIdx = 0
	default:
		return ""
	}

	if addrIdx >= len(call.Args) {
		return ""
	}

	lit, ok := call.Args[addrIdx].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}

	addr := strings.Trim(lit.Value, "`\"")
	if !isHardcodedAddr(addr) {
		return ""
	}

	return fmt.Sprintf(
		"%s(%q, ...) uses a hardcoded bind address -- "+
			"this makes port/host configuration inflexible across environments. "+
			"Consider reading from an env var: os.Getenv(\"PORT\") or os.Getenv(\"ADDR\").",
		fnName, addr)
}

// extractCallName extracts the fully qualified function name from a CallExpr.
func extractCallName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		pkg := ""
		if ident, ok := fn.X.(*ast.Ident); ok {
			pkg = ident.Name
		}
		if pkg != "" {
			return pkg + "." + fn.Sel.Name
		}
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// isHardcodedAddr checks if a string literal is a hardcoded address worth
// flagging. Returns true for ":<devport>", "host:devport", or known risky hosts.
func isHardcodedAddr(addr string) bool {
	if addr == "" {
		return false
	}

	// Check for well-known risky hosts.
	lower := strings.ToLower(addr)
	if lower == "0.0.0.0" || strings.HasPrefix(lower, "0.0.0.0:") {
		return true
	}

	// Extract port from "host:port" or ":port" patterns.
	port := extractPort(addr)
	if port == "" {
		return false
	}
	return devPorts[port]
}

// extractPort pulls the port component from an address string.
func extractPort(addr string) string {
	// Handle [::]:port (IPv6).
	if strings.HasPrefix(addr, "[") {
		if idx := strings.LastIndex(addr, "]:"); idx >= 0 {
			return addr[idx+2:]
		}
		return ""
	}
	// Handle host:port or :port.
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		return addr[idx+1:]
	}
	return ""
}

// --- JS/TS regex-based detection ---

// jstsListenRe matches common JS/TS server listen calls with numeric ports.
var jstsListenRe = regexp.MustCompile(
	`(?i)\.listen\s*\(\s*(\d{2,5})`)

// jstsHostRe matches hardcoded host values in JS/TS configuration.
var jstsHostRe = regexp.MustCompile(
	`(?i)(?:host|hostname)\s*[:=]\s*['"](0\.0\.0\.0|localhost|127\.0\.0\.1)['"]`)

// findJSTSHardcodedHost detects hardcoded ports in JS/TS .listen() calls
// and host assignments.
func findJSTSHardcodedHost(content string) []string {
	var findings []string

	seen := make(map[string]bool)
	for _, m := range jstsListenRe.FindAllStringSubmatch(content, -1) {
		port := m[1]
		if !devPorts[port] {
			continue
		}
		msg := fmt.Sprintf(
			".listen(%s) uses a hardcoded port -- consider reading from "+
				"process.env.PORT or a config module for environment flexibility.", port)
		if !seen[msg] {
			seen[msg] = true
			findings = append(findings, msg)
		}
	}

	for _, m := range jstsHostRe.FindAllStringSubmatch(content, -1) {
		msg := fmt.Sprintf(
			"Hardcoded host %q detected -- use environment configuration "+
				"to avoid binding to unintended interfaces.", m[1])
		if !seen[msg] {
			seen[msg] = true
			findings = append(findings, msg)
		}
	}

	return findings
}

// --- Python regex-based detection ---

// pyRunRe matches Flask/Django app.run() with hardcoded port or host.
var pyRunRe = regexp.MustCompile(
	`(?i)run\s*\(\s*(?:.*?port\s*=\s*(\d{2,5})|.*?host\s*=\s*['"]([^'"\)]+)['"])`)

// pyPortRe matches standalone port assignments in Python.
var pyPortRe = regexp.MustCompile(
	`(?i)\bPORT\s*=\s*(\d{4,5})`)

// findPythonHardcodedHost detects hardcoded ports in Python web frameworks.
func findPythonHardcodedHost(content string) []string {
	var findings []string
	seen := make(map[string]bool)

	for _, m := range pyRunRe.FindAllStringSubmatch(content, -1) {
		if m[1] != "" && devPorts[m[1]] {
			msg := fmt.Sprintf(
				"app.run(port=%s) uses a hardcoded port -- "+
					"use os.environ.get(\"PORT\") for flexibility.", m[1])
			if !seen[msg] {
				seen[msg] = true
				findings = append(findings, msg)
			}
		}
		if m[2] != "" {
			lower := strings.ToLower(m[2])
			if lower == "0.0.0.0" || lower == "localhost" {
				msg := fmt.Sprintf(
					"app.run(host=%q) uses a hardcoded host -- "+
						"use os.environ.get(\"HOST\") for flexibility.", m[2])
				if !seen[msg] {
					seen[msg] = true
					findings = append(findings, msg)
				}
			}
		}
	}

	for _, m := range pyPortRe.FindAllStringSubmatch(content, -1) {
		if devPorts[m[1]] {
			msg := fmt.Sprintf(
				"PORT = %s is hardcoded -- consider os.environ.get(\"PORT\", %s).",
				m[1], m[1])
			if !seen[msg] {
				seen[msg] = true
				findings = append(findings, msg)
			}
		}
	}

	return findings
}
