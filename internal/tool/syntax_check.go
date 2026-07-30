package tool

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// syntaxCheck validates source code syntax for common file types and returns
// a formatted warning string if errors are found. Returns empty string for:
//   - File types without a syntax checker
//   - Valid code
//   - Empty/whitespace-only content
//
// This provides in-process, dependency-free syntax validation that runs on
// every write/edit — unlike postEditDiagnostics (which requires an LSP server)
// or the end-of-turn verify loop (which requires a full build). Catching syntax
// errors at write time saves 3-5 wasted iterations that would otherwise be spent
// on a doomed trajectory (Claude Code and Aider both surface syntax errors
// immediately on edit; this closes that gap for ggcode).
//
// Non-blocking: all checkers run synchronously but complete in <1ms for typical
// files. They never touch the filesystem or network.
func syntaxCheck(path string, data []byte) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".go":
		return syntaxCheckGo(path, data)
	case ".json":
		return syntaxCheckJSON(path, data)
	default:
		return ""
	}
}

// syntaxCheckGo validates Go source using go/parser (stdlib, no type checking).
// This catches syntax errors like missing braces, unclosed strings, invalid
// declarations — the class of errors that cause immediate build failures.
func syntaxCheckGo(path string, data []byte) string {
	fset := token.NewFileSet()
	// SkipObjectResolution avoids unnecessary work — we only need parse-level
	// validation, not full type analysis.
	f, err := parser.ParseFile(fset, filepath.Base(path), data, parser.SkipObjectResolution)
	if err == nil {
		return ""
	}
	// parser.ParseFile can return (nil, err) or (f, err) depending on severity.
	// scanner.ErrorList aggregates multiple errors; extract the first few.
	errStr := err.Error()
	// Trim the file path prefix from error messages for cleaner output.
	base := filepath.Base(path)
	errStr = strings.ReplaceAll(errStr, path, base)
	// Cap error output to keep the tool result manageable.
	if len(errStr) > 500 {
		errStr = errStr[:500] + "..."
	}
	debug.Log("syntax-check", "Go syntax error in %s: %s", base, errStr)
	var b strings.Builder
	b.WriteString("\n\n[Syntax Error — Go code will not compile]\n")
	b.WriteString(errStr)
	b.WriteString("\nFix the syntax error before proceeding to avoid a build failure.")
	if f == nil {
		b.WriteString(" (The file was saved but contains fatal syntax errors — the entire file may need correction.)")
	}
	return b.String()
}

// syntaxCheckJSON validates JSON syntax using encoding/json (stdlib).
// Catches missing commas, unclosed braces, trailing commas, and other common
// JSON errors that would cause silent failures when the file is later parsed.
func syntaxCheckJSON(path string, data []byte) string {
	// Skip empty or whitespace-only files.
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return ""
	}
	if json.Valid(data) {
		return ""
	}
	// Try to get a more specific error message.
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		errStr := err.Error()
		if len(errStr) > 300 {
			errStr = errStr[:300] + "..."
		}
		debug.Log("syntax-check", "JSON syntax error in %s: %s", filepath.Base(path), errStr)
		return fmt.Sprintf("\n\n[Syntax Error — invalid JSON]\n%s\nFix the JSON syntax error before proceeding.", errStr)
	}
	return ""
}
