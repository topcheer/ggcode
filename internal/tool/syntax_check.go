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

func syntaxCheckGo(path string, data []byte) string {
	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, filepath.Base(path), data, parser.SkipObjectResolution)
	if err == nil {
		return ""
	}

	errStr := err.Error()

	base := filepath.Base(path)
	errStr = strings.ReplaceAll(errStr, path, base)

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

func syntaxCheckJSON(path string, data []byte) string {

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return ""
	}
	if json.Valid(data) {
		return ""
	}
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
