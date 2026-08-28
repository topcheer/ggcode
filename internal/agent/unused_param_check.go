package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// checkUnusedParam detects Go function parameters that are never referenced
// in the function body. Unused parameters often indicate dead code, copy-paste
// errors, or incomplete refactoring.
//
// This is a pure AST check -- zero LLM cost.
// Heuristics:
//   - Skip exported functions (API stability may require the param)
//   - Skip test files (fixtures commonly have unused params)
//   - Skip functions with no body or <= 1 statement (stubs)
//   - Skip params named _ (blank identifier, intentionally unused)
//
// #1219 hardening:
//   - Delta-aware: findings already present in oldContent (keyed by
//     function:param, multiset-counted) are not re-flagged on every edit.
//   - Scope-aware usage: the parser's object resolution links each
//     identifier to its declaring object, so an inner shadowing declaration
//     (x := 5) no longer marks an unused outer param as used.
func checkUnusedParam(filePath, oldContent, src string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil || file == nil {
		return nil
	}

	var issues []upIssue
	for _, decl := range file.Decls {
		issues = append(issues, upCheckFunc(fset, filePath, decl)...)
	}

	// Delta-aware filtering (#1219): subtract findings that already existed
	// in the old content. Key is function:param; counts are compared as a
	// multiset so adding a second same-named function (e.g. methods on
	// different receivers) is still reported, while pure line shifts and
	// re-saves of pre-existing findings stay silent.
	if strings.TrimSpace(oldContent) != "" && len(issues) > 0 {
		oldFset := token.NewFileSet()
		oldFile, perr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if perr == nil && oldFile != nil {
			oldCounts := make(map[string]int)
			for _, decl := range oldFile.Decls {
				for _, oi := range upCheckFunc(oldFset, filePath, decl) {
					oldCounts[upIssueKey(oi)]++
				}
			}
			seen := make(map[string]int)
			filtered := issues[:0]
			for _, is := range issues {
				key := upIssueKey(is)
				seen[key]++
				if seen[key] <= oldCounts[key] {
					continue // matched against a pre-existing finding
				}
				filtered = append(filtered, is)
			}
			issues = filtered
		}
	}

	if len(issues) == 0 {
		return nil
	}

	const maxUnusedParamWarnings = 5
	var warnings []string
	for i, is := range issues {
		if i >= maxUnusedParamWarnings {
			warnings = append(warnings,
				fmt.Sprintf("...and more unused parameter(s) in %s", filePath))
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s: parameter '%s' is never used in function '%s' "+
				"-- consider removing it or renaming to '_'",
			upFormatPos(is.pos), is.param, is.funcName))
	}
	return warnings
}

// upIssue records one unused-parameter finding.
type upIssue struct {
	pos      token.Position
	param    string
	funcName string
}

// upIssueKey is the delta-dedup key for a finding. Position-independent so
// line shifts from unrelated edits do not break delta filtering.
func upIssueKey(i upIssue) string {
	return i.funcName + ":" + i.param
}

// upCheckFunc checks a single function declaration for unused parameters.
func upCheckFunc(fset *token.FileSet, filePath string, decl ast.Decl) []upIssue {
	fd, ok := decl.(*ast.FuncDecl)
	if !ok || fd.Body == nil || fd.Type.Params == nil {
		return nil
	}
	// Skip stubs
	if len(fd.Body.List) <= 1 {
		return nil
	}
	// Skip exported functions
	if fd.Name.IsExported() {
		return nil
	}
	// Skip test files
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}

	// Scope-aware usage collection (#1219): the parser's object resolution
	// (active for parser.ParseFile mode 0) links every identifier to its
	// declaring *ast.Object. A parameter counts as used only when some body
	// identifier resolves to the parameter's own object; an inner shadowing
	// declaration (x := 5) creates a distinct object and therefore no longer
	// masks an unused parameter. Identifiers that fail to resolve (nil Obj)
	// are conservatively treated as uses to avoid false positives.
	paramNames := make(map[string]bool)
	for _, field := range fd.Type.Params.List {
		for _, name := range field.Names {
			if name.Name != "_" {
				paramNames[name.Name] = true
			}
		}
	}
	usedObjs := make(map[*ast.Object]bool)
	unresolved := make(map[string]bool)
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || !paramNames[ident.Name] {
			return true
		}
		if ident.Obj != nil {
			usedObjs[ident.Obj] = true
		} else {
			unresolved[ident.Name] = true
		}
		return true
	})

	var issues []upIssue
	for _, field := range fd.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "_" {
				continue
			}
			used := unresolved[name.Name] ||
				(name.Obj != nil && usedObjs[name.Obj])
			if used {
				continue
			}
			issues = append(issues, upIssue{
				pos:      fset.Position(name.Pos()),
				param:    name.Name,
				funcName: fd.Name.Name,
			})
		}
	}
	return issues
}

// upFormatPos formats a token.Position for display.
func upFormatPos(pos token.Position) string {
	return fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
}
