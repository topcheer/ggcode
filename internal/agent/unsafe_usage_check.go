package agent

// Unsafe Package Usage Detection
//
// Problem: AI coding agents frequently suggest `unsafe` package operations
// for "optimization" (zero-copy conversions, manual pointer arithmetic)
// without understanding Go's GC and memory model. The most dangerous patterns:
//
//  1. Pointer arithmetic via uintptr: unsafe.Pointer(uintptr(p) + offset).
//     The GC may move the object between the uintptr() conversion and the
//     unsafe.Pointer() reconversion, creating a dangling pointer. The only
//     safe way is unsafe.Add() (Go 1.17+).
//
//  2. reflect.SliceHeader / reflect.StringHeader usage (deprecated since
//     Go 1.20). Direct manipulation of the .Data field is undefined behavior
//     and may break in future Go versions. Use unsafe.Slice / unsafe.String /
//     unsafe.StringData / unsafe.SliceData instead.
//
//  3. Storing uintptr from unsafe.Pointer: offset := uintptr(unsafe.Pointer(p)).
//     A uintptr is just an integer - the GC does not treat it as a pointer.
//     If the object moves during GC, the stored uintptr becomes stale.
//
// Competitor analysis:
//   - Claude Code: no write-time unsafe detection
//   - Cursor: relies on go vet -unsafeptr (catches only one narrow pattern)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Devin: no detection
//   - go vet -unsafeptr: detects only one pattern (uintptr stored in a
//     variable and converted back to unsafe.Pointer). Misses reflect header
//     misuse and general pointer arithmetic.
//
// Approach: AST-based analysis. Delta-aware: only flags patterns newly
// introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// unsafeInstance represents a detected unsafe package misuse.
type unsafeInstance struct {
	category string // "pointer-arith", "reflect-header", "stored-uintptr"
	detail   string // specific detail
	line     int    // line number (0 if unknown)
}

const (
	unsafeCatPtrArith    = "pointer-arith"
	unsafeCatReflectHdr  = "reflect-header"
	unsafeCatStoredUint  = "stored-uintptr"
	unsafeDetailPtrArith = "unsafe.Pointer arithmetic via uintptr"
	unsafeDetailStored   = "uintptr stored from unsafe.Pointer (not GC-tracked)"
)

// checkUnsafeUsage detects dangerous unsafe package patterns in Go source.
// Returns warnings for newly-introduced issues. Delta-aware.
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

	oldSet := collectUnsafeIssues(oldContent)
	newInstances := findUnsafeIssues(newContent)

	var warnings []string
	reported := make(map[string]bool, len(oldSet))
	for k, v := range oldSet {
		reported[k] = v
	}

	for _, inst := range newInstances {
		key := inst.category + "|" + inst.detail
		if reported[key] {
			continue
		}
		reported[key] = true
		warnings = append(warnings, formatUnsafeWarning(inst))
		if len(warnings) >= 3 {
			break
		}
	}
	return warnings
}

// formatUnsafeWarning converts an unsafeInstance into a human-readable warning.
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

// findUnsafeIssues parses Go source and returns all unsafe misuse instances.
func findUnsafeIssues(src string) []unsafeInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
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

	// Pattern 3: stored uintptr from unsafe.Pointer (requires assignment context).
	ast.Inspect(file, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, rhs := range as.Rhs {
			if isUintptrOfUnsafePointer(rhs) {
				instances = append(instances, unsafeInstance{
					category: unsafeCatStoredUint,
					detail:   unsafeDetailStored,
					line:     fset.Position(as.Pos()).Line,
				})
			}
		}
		return true
	})

	return instances
}

// checkUnsafePointerArith detects unsafe.Pointer(uintptr(...) +/- offset).
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

// hasUintptrInBinaryExpr checks if a binary expression involves uintptr(...)
// conversions combined with + or -.
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

// checkReflectHeader detects reflect.SliceHeader / reflect.StringHeader usage.
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

// isUnsafePointerCall returns true if ce is unsafe.Pointer(...).
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

// isUintptrCall returns true if expr is a bare uintptr(...) conversion.
func isUintptrCall(expr ast.Expr) bool {
	ce, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := ce.Fun.(*ast.Ident)
	return ok && ident.Name == "uintptr"
}

// isUintptrOfUnsafePointer returns true if expr is uintptr(unsafe.Pointer(...)).
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

// collectUnsafeIssues parses old content and returns a set of existing unsafe
// issue signatures for delta-aware suppression.
func collectUnsafeIssues(src string) map[string]bool {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	instances := findUnsafeIssues(src)
	result := make(map[string]bool, len(instances))
	for _, inst := range instances {
		result[inst.category+"|"+inst.detail] = true
	}
	return result
}
