package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"
)

type resourceAcquisition struct {
	pkg           string
	funcs         []string
	resourceHint  string
	cleanupMethod string
	bodyField     bool
}

var resourceAcquisitions = []resourceAcquisition{
	{
		pkg:           "os",
		funcs:         []string{"Open", "Create", "OpenFile"},
		resourceHint:  "file handle",
		cleanupMethod: "Close",
	},
	{
		pkg:           "net",
		funcs:         []string{"Listen"},
		resourceHint:  "network listener",
		cleanupMethod: "Close",
	},
	{
		pkg:           "http",
		funcs:         []string{"Get", "Post", "Head", "PostForm"},
		resourceHint:  "HTTP response body",
		cleanupMethod: "Close",
		bodyField:     true,
	},
}

type resourceLeak struct {
	varName      string
	resourceHint string
	pos          token.Pos
	sig          string
}

type resourceLeakDeltaIndex struct {
	tokens []oldResourceLeakToken
}

type oldResourceLeakToken struct {
	key       string
	suffixKey string
	oldFnName string
	used      bool
}

func (idx *resourceLeakDeltaIndex) matchOldResourceLeak(inst resourceLeak, fnName string, newFnNames map[string]bool) bool {
	exact := fnName + "|" + inst.varName
	for i := range idx.tokens {
		t := &idx.tokens[i]
		if !t.used && t.key == exact {
			t.used = true
			return true
		}
	}
	suffix := inst.sig + "|" + inst.varName
	for i := range idx.tokens {
		t := &idx.tokens[i]
		if t.used || t.suffixKey != suffix {
			continue
		}

		if newFnNames[t.oldFnName] {
			continue
		}
		t.used = true
		return true
	}
	return false
}

func collectOldResourceLeakIndex(filePath, oldContent string) resourceLeakDeltaIndex {
	idx := resourceLeakDeltaIndex{}
	if strings.TrimSpace(oldContent) == "" {
		return idx
	}
	oldFset := token.NewFileSet()
	oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
	if oldErr != nil || oldFile == nil {
		return idx
	}
	for _, decl := range oldFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		acquired := findResourceAcquisitions(fn)
		if len(acquired) == 0 {
			continue
		}
		fnName := "<anonymous>"
		if fn.Name != nil {
			fnName = fn.Name.Name
		}
		cleanedVars := findCleanupCalls(fn)
		for _, acq := range acquired {
			if _, cleaned := cleanedVars[acq.varName]; cleaned {
				continue
			}
			if ownershipTransferred(fn, acq.varName) {
				continue
			}
			idx.tokens = append(idx.tokens, oldResourceLeakToken{
				key:       fnName + "|" + acq.varName,
				suffixKey: acq.sig + "|" + acq.varName,
				oldFnName: fnName,
			})
		}
	}
	return idx
}

func checkResourceLeaks(filePath, oldContent, src string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource(filePath, src, 0)
	if err != nil || file == nil {
		return nil
	}

	oldIdx := collectOldResourceLeakIndex(filePath, oldContent)

	newFnNames := map[string]bool{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil {
			newFnNames[fn.Name.Name] = true
		}
	}

	var warnings []string

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		warnings = append(warnings, newResourceLeakWarnings(fn, fset, &oldIdx, newFnNames)...)
	}

	return warnings
}

func newResourceLeakWarnings(fn *ast.FuncDecl, fset *token.FileSet, oldIdx *resourceLeakDeltaIndex, newFnNames map[string]bool) []string {
	acquired := findResourceAcquisitions(fn)
	if len(acquired) == 0 {
		return nil
	}

	fnName := "<anonymous>"
	if fn.Name != nil {
		fnName = fn.Name.Name
	}

	var warnings []string
	cleanedVars := findCleanupCalls(fn)
	for _, acq := range acquired {
		if _, cleaned := cleanedVars[acq.varName]; cleaned {
			continue
		}

		if ownershipTransferred(fn, acq.varName) {
			continue
		}
		if oldIdx.matchOldResourceLeak(acq, fnName, newFnNames) {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"Possible resource leak: %s acquired (variable %s at %s) but no defer .Close() "+
				"or cleanup call found in the same function. Verify ownership: if this function "+
				"retains the resource, add `defer %s.Close()` immediately after the assignment; "+
				"if %s is returned or handed off to a caller/callee, that side is responsible for "+
				"closing it.",
			acq.resourceHint, acq.varName, fset.Position(acq.pos), acq.varName, acq.varName))
	}
	return warnings
}

func findResourceAcquisitions(fn *ast.FuncDecl) []resourceLeak {
	var leaks []resourceLeak

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for i, expr := range assign.Rhs {
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				continue
			}

			acq := matchResourceCall(call)
			if acq == nil {
				continue
			}

			if i >= len(assign.Lhs) {
				continue
			}

			varName := extractVarName(assign.Lhs[i], acq.bodyField)
			if varName == "" {
				continue
			}

			leaks = append(leaks, resourceLeak{
				varName:      varName,
				resourceHint: acq.resourceHint,
				pos:          call.Pos(),
				sig:          nodeSignature(call),
			})
		}
		return true
	})

	return leaks
}

func nodeSignature(n ast.Node) string {
	var b strings.Builder
	if err := printer.Fprint(&b, token.NewFileSet(), n); err != nil {
		return ""
	}
	return b.String()
}

func ownershipTransferred(fn *ast.FuncDecl, varName string) bool {
	base := varName
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	transferred := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if transferred {
			return false
		}
		switch node := n.(type) {
		case *ast.ReturnStmt:
			for _, res := range node.Results {
				if exprIsResourceHandoff(res, base) {
					transferred = true
					return false
				}
			}
		case *ast.CallExpr:

			consumer := isKnownBodyConsumer(node)
			for _, arg := range node.Args {
				if !exprIsResourceHandoff(arg, base) {
					continue
				}
				if consumer && isCloserFieldRef(arg, base) {
					continue
				}
				transferred = true
				return false
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {

				owner := false
				switch v := lhs.(type) {
				case *ast.SelectorExpr:
					if id, ok := v.X.(*ast.Ident); ok && id.Name == base {
						continue
					}
					owner = true
				case *ast.IndexExpr:
					if id, ok := v.X.(*ast.Ident); ok && id.Name == base {
						continue
					}
					owner = true
				}
				if !owner {
					continue
				}
				for _, rhs := range node.Rhs {
					if exprIsResourceHandoff(rhs, base) {
						transferred = true
						return false
					}
				}
			}
		case *ast.SendStmt:

			if exprIsResourceHandoff(node.Value, base) {
				transferred = true
				return false
			}
		}
		return true
	})
	return transferred
}

func exprIsResourceHandoff(e ast.Expr, name string) bool {
	for {
		if u, ok := e.(*ast.UnaryExpr); ok && (u.Op == token.AND || u.Op == token.MUL) {
			e = u.X
			continue
		}
		break
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name == name
	}
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == name {

			switch sel.Sel.Name {
			case "Body", "File", "Conn", "Handle":
				return true
			}
			return false
		}
	}

	if lit, ok := e.(*ast.CompositeLit); ok {
		for _, elt := range lit.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}
			if exprIsResourceHandoff(elt, name) {
				return true
			}
		}
	}
	return false
}

func isCloserFieldRef(e ast.Expr, name string) bool {
	for {
		if u, ok := e.(*ast.UnaryExpr); ok && (u.Op == token.AND || u.Op == token.MUL) {
			e = u.X
			continue
		}
		break
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != name {
		return false
	}
	switch sel.Sel.Name {
	case "Body", "File", "Conn", "Handle":
		return true
	}
	return false
}

var bodyConsumerFuncs = map[string]bool{
	"Copy":        true,
	"CopyBuffer":  true,
	"CopyN":       true,
	"ReadAll":     true,
	"ReadFull":    true,
	"ReadAtLeast": true,
	"NewDecoder":  true,
	"NewReader":   true,
	"NewScanner":  true,
	"TeeReader":   true,
	"MultiReader": true,
}

func isKnownBodyConsumer(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return bodyConsumerFuncs[fn.Sel.Name]
	case *ast.Ident:
		return bodyConsumerFuncs[fn.Name]
	}
	return false
}

func matchResourceCall(call *ast.CallExpr) *resourceAcquisition {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}

	for i := range resourceAcquisitions {
		acq := &resourceAcquisitions[i]
		if pkgIdent.Name != acq.pkg {
			continue
		}
		for _, fn := range acq.funcs {
			if sel.Sel.Name == fn {
				return acq
			}
		}
	}
	return nil
}

func extractVarName(expr ast.Expr, bodyField bool) string {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	if bodyField {
		return ident.Name + ".Body"
	}
	return ident.Name
}

func findCleanupCalls(fn *ast.FuncDecl) map[string]bool {
	cleaned := make(map[string]bool)

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		var call *ast.CallExpr

		if d, ok := node.(*ast.DeferStmt); ok {
			call = d.Call
		}
		if c, ok := node.(*ast.CallExpr); ok {
			call = c
		}

		if call == nil {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if !isCleanupMethod(sel.Sel.Name) {
			return true
		}

		var varName string
		switch x := sel.X.(type) {
		case *ast.Ident:
			varName = x.Name
		case *ast.SelectorExpr:
			if inner, ok := x.X.(*ast.Ident); ok {
				varName = inner.Name + "." + x.Sel.Name
			}
		}

		if varName != "" {
			cleaned[varName] = true
		}

		return true
	})

	return cleaned
}

var cleanupMethods = map[string]bool{
	"Close":    true,
	"CloseAll": true,
	"Cleanup":  true,
	"Release":  true,
	"Free":     true,
	"Shutdown": true,
	"Stop":     true,
	"Unlock":   true,
	"RUnlock":  true,
}

func isCleanupMethod(name string) bool {
	return cleanupMethods[name]
}
