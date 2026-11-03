package agent

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	idMaxInterfaceMethods = 7

	idMaxWarnings = 5

	idMinMethodsForSingleImpl = 2
)

var idGenericMethodNames = map[string]bool{
	"Do": true, "Run": true, "Execute": true, "Process": true,
	"Handle": true, "Perform": true, "Call": true, "Apply": true,
	"Work": true, "Action": true, "Operate": true, "Invoke": true,
}

type idInterfaceInfo struct {
	MethodCount   int
	MethodNames   []string
	HasUnexported bool
	UsesAny       bool
}

type idFuncReturnInfo struct {
	ReturnsAny bool
}

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
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"Interface '%s' has %d methods (>%d threshold): violates the Interface Segregation Principle (ISP). "+
					"Large interfaces force implementers to satisfy methods they may not need. Consider splitting into smaller, focused interfaces.",
				ts.Name.Name, count, idMaxInterfaceMethods))
		}
	}
	return warnings
}

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

func idNamingIssue(ifaceName, methodName string) string {
	lowerIface := strings.ToLower(ifaceName)
	lowerMethod := strings.ToLower(methodName)

	if strings.HasPrefix(lowerIface, lowerMethod) {
		return ""
	}
	if idGenericMethodNames[methodName] {
		return fmt.Sprintf(
			"Single-method interface '%s' uses generic method '%s' that doesn't match its name. "+
				"Go convention: 'Reader { Read() }', 'Closer { Close() }'. Use a descriptive verb matching the interface purpose.",
			ifaceName, methodName)
	}
	return ""
}

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

func idCheckReturningAny(file *ast.File, oldFuncs map[string]idFuncReturnInfo) []string {
	var warnings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Type == nil || fn.Type.Results == nil || fn.Name == nil {
			continue
		}
		if !fn.Name.IsExported() {
			continue
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

func idCountMethods(it *ast.InterfaceType) int {
	if it.Methods == nil {
		return 0
	}
	count := 0
	for _, field := range it.Methods.List {
		if len(field.Names) > 0 {
			count += len(field.Names)
		} else {
			count++
		}
	}
	return count
}

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

func idIsAnyType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "any"
	case *ast.InterfaceType:
		return t.Methods == nil || len(t.Methods.List) == 0
	}
	return false
}

func idResultsReturnAny(results []*ast.Field) bool {
	for _, field := range results {
		if idIsAnyType(field.Type) {
			return true
		}
	}
	return false
}

func idRecvTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}

	if index, ok := expr.(*ast.IndexExpr); ok {
		return gofmtExpr(index)
	}
	return ""
}

func gofmtExpr(expr ast.Expr) string {
	var buf bytes.Buffer
	fset := token.NewFileSet()
	p := printer.Config{Mode: printer.UseSpaces, Tabwidth: 8}
	if err := p.Fprint(&buf, fset, expr); err != nil {
		return ""
	}
	return buf.String()
}

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

func idCountImplementers(ifaceMethods map[string]bool, typeMethods map[string]map[string]bool) int {
	count := 0
	for _, methods := range typeMethods {
		if idImplementsAll(ifaceMethods, methods) {
			count++
		}
	}
	return count
}

func idImplementsAll(ifaceMethods, typeMethods map[string]bool) bool {
	for m := range ifaceMethods {
		if !typeMethods[m] {
			return false
		}
	}
	return true
}

func idExtractInterfaces(src string) map[string]idInterfaceInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	file, _, err := parseGoSource("", src, 0)
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

func idExtractFuncReturns(src string) map[string]idFuncReturnInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	file, _, err := parseGoSource("", src, 0)
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
