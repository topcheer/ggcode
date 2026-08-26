package agent

// interface_design_check.go implements Go Interface Design & Abstraction
// Intelligence -- deterministic detection of interface design anti-patterns
// in Go source files after a write or edit.
//
// This catches design smells that harm maintainability and type safety but
// that the Go compiler cannot detect:
//
//  1. Fat Interface -- interface with too many methods (>7), violating the
//     Interface Segregation Principle (ISP). Large interfaces force
//     implementers to satisfy methods they don't need.
//  2. Non-idiomatic single-method interface naming -- a single-method
//     interface whose method name is a generic verb (Do, Run, Execute, ...)
//     that doesn't derive from the interface name. Go convention: Reader →
//     Read, Closer → Close.
//  3. Returning any/interface{} -- exported functions returning `any` or
//     `interface{}` erase type safety at every call site.
//  4. Interface method using any/interface{} -- interface methods with `any`
//     params or returns weaken the type contract.
//  5. Exported interface with unexported method -- limits implementations to
//     the current package, defeating the purpose of an exported interface.
//  6. Single implementation (unnecessary abstraction) -- a newly-introduced
//     interface with >=2 methods that has only one implementer in the
//     package. Go proverb: "The bigger the interface, the weaker the
//     abstraction."
//
// All checks are:
//   - Delta-aware: only report issues newly introduced by the edit (compares
//     old vs new content per interface/function).
//   - Deterministic: pure AST analysis, zero LLM cost.
//   - Non-blocking: warnings are advisory, edits are not reverted.
//   - Bounded: at most idMaxWarnings per file.
//
// Competitor mapping:
//   - golangci-lint: `interfacebloat` (fat interface), `iface` analyzer --
//     run on-demand, not at write time.
//   - Claude Code / Cursor / Copilot: rely on post-build tools or LSP.
//   - OpenHands / Devin: no interface design analysis.
//   - Our advantage: real-time, delta-aware, zero-config detection at write
//     time, before the developer even runs a linter.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// idMaxInterfaceMethods is the ISP threshold. Interfaces exceeding this
	// method count should be split into smaller, focused interfaces.
	idMaxInterfaceMethods = 7

	// idMaxWarnings caps the number of interface-design warnings per file to
	// avoid overwhelming the agent's context.
	idMaxWarnings = 5

	// idMinMethodsForSingleImpl is the minimum method count for the
	// single-implementation check. Interfaces with fewer methods are commonly
	// used for mocking/testing and are not flagged.
	idMinMethodsForSingleImpl = 2
)

// idGenericMethodNames lists overly generic method names that reduce interface
// clarity. A single-method interface using one of these names should use a more
// descriptive verb instead (e.g., Reader → Read, not Reader → Do).
var idGenericMethodNames = map[string]bool{
	"Do": true, "Run": true, "Execute": true, "Process": true,
	"Handle": true, "Perform": true, "Call": true, "Apply": true,
	"Work": true, "Action": true, "Operate": true, "Invoke": true,
}

// idInterfaceInfo captures design-relevant metadata about an interface,
// extracted from source for delta-aware comparison.
type idInterfaceInfo struct {
	MethodCount   int
	MethodNames   []string
	HasUnexported bool
	UsesAny       bool
}

// idFuncReturnInfo captures whether a function returns any/interface{}.
type idFuncReturnInfo struct {
	ReturnsAny bool
}

// checkInterfaceDesign is the entry point for interface design checks. It uses
// the pre-parsed GoAST from CheckContext and compares against old content for
// delta-awareness. Returns a slice of advisory warning strings.
func checkInterfaceDesign(ctx CheckContext) []string {
	if ctx.GoAST == nil {
		return nil
	}
	if strings.HasSuffix(ctx.FilePath, "_test.go") {
		return nil
	}

	oldIfaces := idExtractInterfaces(ctx.OldContent)
	oldFuncs := idExtractFuncReturns(ctx.OldContent)

	var warnings []string
	warnings = append(warnings, idCheckFatInterfaces(ctx.GoAST, oldIfaces)...)
	warnings = append(warnings, idCheckNaming(ctx.GoAST, oldIfaces)...)
	warnings = append(warnings, idCheckInterfaceAny(ctx.GoAST, oldIfaces)...)
	warnings = append(warnings, idCheckUnexportedMethod(ctx.GoAST, oldIfaces)...)
	warnings = append(warnings, idCheckReturningAny(ctx.GoAST, oldFuncs)...)
	warnings = append(warnings, idCheckSingleImpl(ctx, oldIfaces)...)

	if len(warnings) > idMaxWarnings {
		warnings = warnings[:idMaxWarnings]
	}
	if len(warnings) > 0 {
		debug.Log("interface-design", "detected %d issue(s) in %s", len(warnings), ctx.FilePath)
	}
	return warnings
}

// ---------------------------------------------------------------------------
// Check 1: Fat Interface (ISP violation)
// ---------------------------------------------------------------------------

// idCheckFatInterfaces warns about interfaces exceeding the ISP method threshold.
func idCheckFatInterfaces(file *ast.File, oldIfaces map[string]idInterfaceInfo) []string {
	var warnings []string
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok || it.Methods == nil {
				continue
			}
			count := idCountMethods(it)
			if count <= idMaxInterfaceMethods {
				continue
			}
			if old, exists := oldIfaces[ts.Name.Name]; exists && old.MethodCount > idMaxInterfaceMethods {
				continue // already fat before the edit -- delta-aware skip
			}
			warnings = append(warnings, fmt.Sprintf(
				"Interface '%s' has %d methods (>%d threshold): violates the Interface Segregation Principle (ISP). "+
					"Large interfaces force implementers to satisfy methods they may not need. Consider splitting into smaller, focused interfaces.",
				ts.Name.Name, count, idMaxInterfaceMethods))
		}
	}
	return warnings
}

// ---------------------------------------------------------------------------
// Check 2: Non-idiomatic single-method interface naming
// ---------------------------------------------------------------------------

// idCheckNaming warns about single-method interfaces using generic method names
// that don't derive from the interface name.
func idCheckNaming(file *ast.File, oldIfaces map[string]idInterfaceInfo) []string {
	var warnings []string
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok || it.Methods == nil {
				continue
			}
			names := idMethodNames(it)
			if len(names) != 1 {
				continue
			}
			ifaceName, methodName := ts.Name.Name, names[0]
			if old, exists := oldIfaces[ifaceName]; exists && idSliceEqual(old.MethodNames, names) {
				continue
			}
			if msg := idNamingIssue(ifaceName, methodName); msg != "" {
				warnings = append(warnings, msg)
			}
		}
	}
	return warnings
}

// idNamingIssue returns a warning for non-idiomatic naming, or "" if OK.
// Idiomatic single-method interfaces have method names that are prefixes of the
// interface name (Read → Reader, Close → Closer).
func idNamingIssue(ifaceName, methodName string) string {
	lowerIface := strings.ToLower(ifaceName)
	lowerMethod := strings.ToLower(methodName)

	if strings.HasPrefix(lowerIface, lowerMethod) {
		return "" // idiomatic: method derives from interface name
	}
	if idGenericMethodNames[methodName] {
		return fmt.Sprintf(
			"Single-method interface '%s' uses generic method '%s' that doesn't match its name. "+
				"Go convention: 'Reader { Read() }', 'Closer { Close() }'. Use a descriptive verb matching the interface purpose.",
			ifaceName, methodName)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Check 3: Interface method using any/interface{}
// ---------------------------------------------------------------------------

// idCheckInterfaceAny warns about interface methods using any/interface{} in
// params or returns, which weakens the type contract.
func idCheckInterfaceAny(file *ast.File, oldIfaces map[string]idInterfaceInfo) []string {
	var warnings []string
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok || it.Methods == nil {
				continue
			}
			if !idInterfaceUsesAny(it) {
				continue
			}
			if old, exists := oldIfaces[ts.Name.Name]; exists && old.UsesAny {
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"Interface '%s' has a method using 'any' (interface{}) in params or returns: this weakens the type contract at the abstraction boundary. "+
					"Consider a concrete type or a specific interface for stronger compile-time safety.",
				ts.Name.Name))
		}
	}
	return warnings
}

// ---------------------------------------------------------------------------
// Check 4: Exported interface with unexported method
// ---------------------------------------------------------------------------

// idCheckUnexportedMethod warns about exported interfaces containing unexported
// methods, which restricts implementations to the current package.
func idCheckUnexportedMethod(file *ast.File, oldIfaces map[string]idInterfaceInfo) []string {
	var warnings []string
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil || !ts.Name.IsExported() {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok || it.Methods == nil {
				continue
			}
			if !idInterfaceHasUnexportedMethod(it) {
				continue
			}
			if old, exists := oldIfaces[ts.Name.Name]; exists && old.HasUnexported {
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"Exported interface '%s' has an unexported method: this restricts implementations to the current package, "+
					"defeating the purpose of exporting the interface. Make the method exported or unexport the interface.",
				ts.Name.Name))
		}
	}
	return warnings
}

// ---------------------------------------------------------------------------
// Check 5: Returning any/interface{}
// ---------------------------------------------------------------------------

// idCheckReturningAny warns about exported functions/methods returning
// any/interface{}, which erases type safety at call sites.
func idCheckReturningAny(file *ast.File, oldFuncs map[string]idFuncReturnInfo) []string {
	var warnings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Type == nil || fn.Type.Results == nil || fn.Name == nil {
			continue
		}
		if !fn.Name.IsExported() {
			continue // only flag exported functions
		}
		if !idResultsReturnAny(fn.Type.Results.List) {
			continue
		}
		key := idFuncKey(fn)
		if old, exists := oldFuncs[key]; exists && old.ReturnsAny {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"Function '%s' returns 'any' (interface{}): this erases type safety at every call site. "+
				"Consider returning a concrete type or a specific interface.",
			idFuncDisplayName(fn)))
	}
	return warnings
}

// ---------------------------------------------------------------------------
// Check 6: Single implementation (unnecessary abstraction)
// ---------------------------------------------------------------------------

// idCheckSingleImpl warns about newly-introduced interfaces with >=2 methods
// that have only one implementation in the package.
func idCheckSingleImpl(ctx CheckContext, oldIfaces map[string]idInterfaceInfo) []string {
	newIfaces := idFindNewInterfaces(ctx.GoAST, oldIfaces)
	if len(newIfaces) == 0 {
		return nil
	}
	allTypes := idCollectPackageTypes(ctx)
	if len(allTypes) == 0 {
		return nil
	}

	var warnings []string
	for name, methods := range newIfaces {
		if len(methods) < idMinMethodsForSingleImpl {
			continue
		}
		implCount := idCountImplementers(methods, allTypes)
		if implCount == 1 {
			warnings = append(warnings, fmt.Sprintf(
				"Interface '%s' has only 1 implementation in this package. "+
					"If decoupling or testability isn't a goal, a concrete type may be simpler (Go proverb: 'accept interfaces, return structs').",
				name))
		}
	}
	return warnings
}

// idCollectPackageTypes gathers type→methods maps from the package directory
// (excluding the current file) plus the current file's own methods.
func idCollectPackageTypes(ctx CheckContext) map[string]map[string]bool {
	pkgTypes := scanPackageTypeMethods(filepath.Dir(ctx.FilePath), ctx.FilePath, "")
	allTypes := make(map[string]map[string]bool)
	for k, v := range pkgTypes {
		allTypes[k] = v
	}
	for k, v := range idScanFileMethods(ctx.GoAST) {
		allTypes[k] = v
	}
	return allTypes
}

// ---------------------------------------------------------------------------
// AST helper functions
// ---------------------------------------------------------------------------

// idCountMethods counts named methods plus embedded interfaces in an interface
// type. Embedded interfaces are counted as 1 each (conservative lower bound
// since their actual method count requires type-checking).
func idCountMethods(it *ast.InterfaceType) int {
	if it.Methods == nil {
		return 0
	}
	count := 0
	for _, field := range it.Methods.List {
		if len(field.Names) > 0 {
			count += len(field.Names)
		} else {
			count++ // embedded interface
		}
	}
	return count
}

// idMethodNames returns the names of named methods in an interface.
func idMethodNames(it *ast.InterfaceType) []string {
	if it.Methods == nil {
		return nil
	}
	var names []string
	for _, field := range it.Methods.List {
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

// idInterfaceHasUnexportedMethod checks if an interface has any unexported
// named methods.
func idInterfaceHasUnexportedMethod(it *ast.InterfaceType) bool {
	if it.Methods == nil {
		return false
	}
	for _, field := range it.Methods.List {
		for _, name := range field.Names {
			if !name.IsExported() {
				return true
			}
		}
	}
	return false
}

// idInterfaceUsesAny checks if any named method in the interface uses
// any/interface{} in its params or returns.
func idInterfaceUsesAny(it *ast.InterfaceType) bool {
	if it.Methods == nil {
		return false
	}
	for _, field := range it.Methods.List {
		ft, ok := field.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		if idFuncTypeUsesAny(ft) {
			return true
		}
	}
	return false
}

// idFuncTypeUsesAny checks if a function type uses any/interface{} in params
// or returns.
func idFuncTypeUsesAny(ft *ast.FuncType) bool {
	if ft.Params != nil {
		for _, p := range ft.Params.List {
			if idIsAnyType(p.Type) {
				return true
			}
		}
	}
	if ft.Results != nil {
		for _, r := range ft.Results.List {
			if idIsAnyType(r.Type) {
				return true
			}
		}
	}
	return false
}

// idIsAnyType checks if an expression represents `any` or `interface{}`.
func idIsAnyType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "any"
	case *ast.InterfaceType:
		return t.Methods == nil || len(t.Methods.List) == 0
	}
	return false
}

// idResultsReturnAny checks if any return type is bare any/interface{}.
func idResultsReturnAny(results []*ast.Field) bool {
	for _, field := range results {
		if idIsAnyType(field.Type) {
			return true
		}
	}
	return false
}

// idRecvTypeName extracts the receiver type name, stripping pointer indirection.
// Handles generic receivers like `func (f *Foo[T]) M()` by preserving the
// IndexExpr form (Foo[T]) instead of returning empty string (#1066).
func idRecvTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	// #1066: Handle generic receivers like *Foo[T] or Foo[T]
	if index, ok := expr.(*ast.IndexExpr); ok {
		return gofmtExpr(index) // returns "Foo[T]" for idFuncKey
	}
	return ""
}

// gofmtExpr formats an AST expression back to Go source code.
// Used for generic receiver names like "Foo[T]" (#1066).
func gofmtExpr(expr ast.Expr) string {
	var buf bytes.Buffer
	fset := token.NewFileSet()
	p := printer.Config{Mode: printer.UseSpaces, Tabwidth: 8}
	if err := p.Fprint(&buf, fset, expr); err != nil {
		return ""
	}
	return buf.String()
}

// idFuncKey generates a unique key for a function (receiver type + name).
func idFuncKey(fn *ast.FuncDecl) string {
	if fn.Name == nil {
		return ""
	}
	recv := ""
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv = idRecvTypeName(fn.Recv.List[0].Type) + "."
	}
	return recv + fn.Name.Name
}

// idFuncDisplayName returns a human-readable function name.
func idFuncDisplayName(fn *ast.FuncDecl) string {
	if fn.Name == nil {
		return "<anonymous>"
	}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		if rt := idRecvTypeName(fn.Recv.List[0].Type); rt != "" {
			return rt + "." + fn.Name.Name
		}
	}
	return fn.Name.Name
}

// idFindNewInterfaces returns method sets for interfaces not present in oldIfaces.
func idFindNewInterfaces(file *ast.File, oldIfaces map[string]idInterfaceInfo) map[string]map[string]bool {
	result := make(map[string]map[string]bool)
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok || it.Methods == nil {
				continue
			}
			if _, exists := oldIfaces[ts.Name.Name]; exists {
				continue
			}
			result[ts.Name.Name] = idMethodSet(it)
		}
	}
	return result
}

// idMethodSet extracts method names from an interface as a set.
func idMethodSet(it *ast.InterfaceType) map[string]bool {
	methods := make(map[string]bool)
	if it.Methods == nil {
		return methods
	}
	for _, field := range it.Methods.List {
		for _, name := range field.Names {
			methods[name.Name] = true
		}
	}
	return methods
}

// idScanFileMethods scans an AST file for receiver type → method name set.
func idScanFileMethods(file *ast.File) map[string]map[string]bool {
	result := make(map[string]map[string]bool)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		recvType := idRecvTypeName(fn.Recv.List[0].Type)
		if recvType == "" {
			continue
		}
		if _, ok := result[recvType]; !ok {
			result[recvType] = make(map[string]bool)
		}
		result[recvType][fn.Name.Name] = true
	}
	return result
}

// idCountImplementers counts types that implement all methods in ifaceMethods.
func idCountImplementers(ifaceMethods map[string]bool, typeMethods map[string]map[string]bool) int {
	count := 0
	for _, methods := range typeMethods {
		if idImplementsAll(ifaceMethods, methods) {
			count++
		}
	}
	return count
}

// idImplementsAll checks if a type's method set covers all interface methods.
func idImplementsAll(ifaceMethods, typeMethods map[string]bool) bool {
	for m := range ifaceMethods {
		if !typeMethods[m] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Old-content extraction (for delta-aware comparison)
// ---------------------------------------------------------------------------

// idExtractInterfaces parses source and returns interface info by name.
func idExtractInterfaces(src string) map[string]idInterfaceInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}
	result := make(map[string]idInterfaceInfo)
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok || it.Methods == nil {
				continue
			}
			result[ts.Name.Name] = idInterfaceInfo{
				MethodCount:   idCountMethods(it),
				MethodNames:   idMethodNames(it),
				HasUnexported: idInterfaceHasUnexportedMethod(it),
				UsesAny:       idInterfaceUsesAny(it),
			}
		}
	}
	return result
}

// idExtractFuncReturns parses source and returns function return info by key.
func idExtractFuncReturns(src string) map[string]idFuncReturnInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}
	result := make(map[string]idFuncReturnInfo)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Type == nil || fn.Type.Results == nil || fn.Name == nil {
			continue
		}
		key := idFuncKey(fn)
		if key == "" {
			continue
		}
		result[key] = idFuncReturnInfo{
			ReturnsAny: idResultsReturnAny(fn.Type.Results.List),
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Utility functions
// ---------------------------------------------------------------------------

// idSliceEqual checks if two string slices are equal.
func idSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
