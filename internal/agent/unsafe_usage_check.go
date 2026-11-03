package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

type unsafeInstance struct {
	category string
	detail   string
	line     int
}

const (
	unsafeCatPtrArith    = "pointer-arith"
	unsafeCatReflectHdr  = "reflect-header"
	unsafeCatStoredUint  = "stored-uintptr"
	unsafeDetailPtrArith = "unsafe.Pointer arithmetic via uintptr"
	unsafeDetailStored   = "uintptr stored from unsafe.Pointer (not GC-tracked)"
)

func checkUnsafeUsage(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldCount := countUnsafeIssues(oldContent)
	newInstances := findUnsafeIssues(newContent)

	var warnings []string
	seen := make(map[string]int, len(oldCount))
	for _, inst := range newInstances {
		key := inst.category + "|" + inst.detail
		seen[key]++
		if seen[key] <= oldCount[key] {
			continue
		}
		warnings = append(warnings, formatUnsafeWarning(inst))
		if len(warnings) >= 3 {
			break
		}
	}
	return warnings
}

func formatUnsafeWarning(inst unsafeInstance) string {
	loc := ""
	if inst.line > 0 {
		loc = fmt.Sprintf(" (line %d)", inst.line)
	}
	switch inst.category {
	case unsafeCatPtrArith:
		return fmt.Sprintf(
			"[Unsafe] Pointer arithmetic%s: %s. The GC may move the object between "+
				"the uintptr conversion and the unsafe.Pointer reconversion, creating a "+
				"dangling pointer. Use unsafe.Add() (Go 1.17+) for safe offset arithmetic.",
			loc, inst.detail)
	case unsafeCatReflectHdr:
		return fmt.Sprintf(
			"[Unsafe] Deprecated reflect header%s: %s is deprecated since Go 1.20. "+
				"Direct .Data field manipulation is undefined behavior. Use "+
				"unsafe.Slice/unsafe.String/unsafe.SliceData/unsafe.StringData instead.",
			loc, inst.detail)
	case unsafeCatStoredUint:
		return fmt.Sprintf(
			"[Unsafe] Stored uintptr%s: %s. A uintptr is not tracked by the GC - "+
				"if the object moves, the stored value becomes a dangling reference. "+
				"Keep values as unsafe.Pointer, not uintptr.",
			loc, inst.detail)
	}
	return ""
}

func findUnsafeIssues(src string) []unsafeInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []unsafeInstance

	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			instances = append(instances, checkUnsafePointerArith(fset, n)...)
		case *ast.SelectorExpr:
			instances = append(instances, checkReflectHeader(fset, n)...)
		}
		return true
	})

	uintptrIdents := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, rhs := range as.Rhs {
			stored := isUintptrOfUnsafePointer(rhs)
			if !stored {

				stored = containsUintptrOfUnsafePointer(rhs)
			}
			if stored {
				instances = append(instances, unsafeInstance{
					category: unsafeCatStoredUint,
					detail:   unsafeDetailStored,
					line:     fset.Position(as.Pos()).Line,
				})

				if len(as.Lhs) == 1 {
					if id, ok := as.Lhs[0].(*ast.Ident); ok {
						uintptrIdents[id.Name] = true
					}
				}
			}
		}
		return true
	})

	ast.Inspect(file, func(node ast.Node) bool {
		ce, ok := node.(*ast.CallExpr)
		if !ok || !isUnsafePointerCall(ce) || len(ce.Args) == 0 {
			return true
		}
		if id, ok := ce.Args[0].(*ast.Ident); ok && uintptrIdents[id.Name] {
			instances = append(instances, unsafeInstance{
				category: unsafeCatPtrArith,
				detail:   unsafeDetailPtrArith,
				line:     fset.Position(ce.Pos()).Line,
			})
		}
		return true
	})

	return instances
}

func checkUnsafePointerArith(fset *token.FileSet, ce *ast.CallExpr) []unsafeInstance {
	if !isUnsafePointerCall(ce) || len(ce.Args) == 0 {
		return nil
	}
	if hasUintptrInBinaryExpr(ce.Args[0]) {
		return []unsafeInstance{{
			category: unsafeCatPtrArith,
			detail:   unsafeDetailPtrArith,
			line:     fset.Position(ce.Pos()).Line,
		}}
	}
	return nil
}

func hasUintptrInBinaryExpr(expr ast.Expr) bool {
	be, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if be.Op != token.ADD && be.Op != token.SUB {
		return false
	}
	if isUintptrCall(be.X) || isUintptrCall(be.Y) {
		return true
	}
	return hasUintptrInBinaryExpr(be.X) || hasUintptrInBinaryExpr(be.Y)
}

func checkReflectHeader(fset *token.FileSet, se *ast.SelectorExpr) []unsafeInstance {
	if se.Sel.Name != "SliceHeader" && se.Sel.Name != "StringHeader" {
		return nil
	}
	pkg, ok := se.X.(*ast.Ident)
	if !ok || pkg.Name != "reflect" {
		return nil
	}
	return []unsafeInstance{{
		category: unsafeCatReflectHdr,
		detail:   "reflect." + se.Sel.Name,
		line:     fset.Position(se.Pos()).Line,
	}}
}

func isUnsafePointerCall(ce *ast.CallExpr) bool {
	se, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := se.X.(*ast.Ident)
	if !ok || pkg.Name != "unsafe" {
		return false
	}
	return se.Sel.Name == "Pointer"
}

func isUintptrCall(expr ast.Expr) bool {
	ce, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := ce.Fun.(*ast.Ident)
	return ok && ident.Name == "uintptr"
}

func containsUintptrOfUnsafePointer(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if isUintptrOfUnsafePointer(expr) {
		return true
	}
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		return containsUintptrOfUnsafePointer(e.X) || containsUintptrOfUnsafePointer(e.Y)
	case *ast.ParenExpr:
		return containsUintptrOfUnsafePointer(e.X)
	case *ast.UnaryExpr:
		return containsUintptrOfUnsafePointer(e.X)
	case *ast.CallExpr:
		for _, a := range e.Args {
			if containsUintptrOfUnsafePointer(a) {
				return true
			}
		}
	}
	return false
}

func isUintptrOfUnsafePointer(expr ast.Expr) bool {
	ce, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := ce.Fun.(*ast.Ident)
	if !ok || ident.Name != "uintptr" {
		return false
	}
	if len(ce.Args) == 0 {
		return false
	}
	inner, ok := ce.Args[0].(*ast.CallExpr)
	return ok && isUnsafePointerCall(inner)
}

func countUnsafeIssues(src string) map[string]int {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	counts := make(map[string]int)
	for _, inst := range findUnsafeIssues(src) {
		counts[inst.category+"|"+inst.detail]++
	}
	return counts
}
