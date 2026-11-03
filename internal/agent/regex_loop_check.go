package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
)

var regexCompileFuncs = map[string]bool{
	"regexp.Compile":          true,
	"regexp.MustCompile":      true,
	"regexp.CompilePOSIX":     true,
	"regexp.MustCompilePOSIX": true,
}

type regexLoopIssue struct {
	funcName string
	pattern  string
	pos      token.Pos
}

func (i regexLoopIssue) fingerprint() string {
	return i.funcName + "|" + i.pattern
}

func checkRegexLoop(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newIssues := findRegexInLoops(filePath, newContent)
	if len(newIssues) == 0 {
		return nil
	}

	if strings.TrimSpace(oldContent) != "" {

		oldFPs := make(map[string]int)
		for _, oi := range findRegexInLoops(filePath, oldContent) {
			oldFPs[oi.fingerprint()]++
		}
		if len(oldFPs) > 0 {
			fresh := newIssues[:0]
			for _, ni := range newIssues {
				if oldFPs[ni.fingerprint()] > 0 {
					oldFPs[ni.fingerprint()]--
					continue
				}
				fresh = append(fresh, ni)
			}
			newIssues = fresh
			if len(newIssues) == 0 {
				return nil
			}
		}
	}

	_, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil {
		return []string{fmt.Sprintf(
			"Detected %d regexp.Compile/MustCompile call(s) inside a loop in %s. "+
				"Regex patterns are recompiled on every iteration (O(m) each). "+
				"Move compilation to package level: `var re = regexp.MustCompile(pattern)`.",
			len(newIssues), filepath.Base(filePath))}
	}

	var warnings []string
	for i, issue := range newIssues {
		if i >= 2 {
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s call inside loop at %s:%d. The regex is recompiled on every "+
				"iteration. Move compilation outside the loop (e.g., to a package-level "+
				"var) and reuse the compiled *regexp.Regexp.",
			issue.funcName, filepath.Base(filePath), fset.Position(issue.pos).Line))
	}
	if extra := len(newIssues) - 2; extra > 0 {
		warnings = append(warnings,
			fmt.Sprintf("...and %d more regex-compile-in-loop pattern(s) in %s",
				extra, filepath.Base(filePath)))
	}
	return warnings
}

func findRegexInLoops(filename, src string) []regexLoopIssue {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	file, _, err := parseGoSource(filename, src, 0)
	if err != nil {
		return nil
	}

	var results []regexLoopIssue

	alias := make(map[string]string)
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path != "regexp" {
			continue
		}
		local := "regexp"
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local != "." && local != "_" {
			alias[local] = "regexp"
		}
	}
	canonical := func(name string) string {
		if i := strings.IndexByte(name, '.'); i > 0 {
			if base, ok := alias[name[:i]]; ok {
				return base + name[i:]
			}
		}
		return name
	}

	seenPos := make(map[token.Pos]bool)

	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch node := n.(type) {
		case *ast.ForStmt:
			body = node.Body
		case *ast.RangeStmt:
			body = node.Body
		default:
			return true
		}
		if body == nil {
			return true
		}

		ast.Inspect(body, func(inner ast.Node) bool {
			if _, isFuncLit := inner.(*ast.FuncLit); isFuncLit {
				return false
			}
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := canonical(callFuncName(call))
			if regexCompileFuncs[name] {
				if seenPos[call.Pos()] {
					return true
				}
				seenPos[call.Pos()] = true
				pattern := ""
				if len(call.Args) > 0 {
					pattern = types.ExprString(call.Args[0])
				}
				results = append(results, regexLoopIssue{
					funcName: name,
					pattern:  pattern,
					pos:      call.Pos(),
				})
			}
			return true
		})
		return true
	})

	return results
}
