package agent

// Breaking change detection for exported Go symbols.
//
// Research basis: The #1 multi-file refactoring failure mode for AI agents is
// modifying an exported symbol's signature (function params, struct fields,
// interface methods) without updating all callers. Claude Code, Cursor, and Cline
// rely on LSP diagnostics that only fire AFTER the build breaks - by then the
// agent has wasted an iteration. Aider avoids this by requiring explicit
// confirmation for multi-file changes.
//
// This check takes a different approach: it compares the AST of the file BEFORE
// and AFTER the edit, detecting when exported symbols have been modified. When a
// signature change is detected, it emits a warning directing the agent to verify
// all callers are updated. This catches the root cause (the signature change)
// rather than the symptom (broken callers), enabling the agent to proactively
// search for and update dependent code in the same turn.
//
// Detection scope:
//  1. Exported functions: parameter list change (count, types)
//  2. Exported functions: return value list change
//  3. Exported methods: same as functions (identified by receiver)
//  4. Exported types: struct field list change (add/remove/rename/reorder)
//  5. Exported interfaces: method set change
//  6. Exported type aliases: underlying type change
//  7. Exported variables/constants: type or category change

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// checkBreakingChanges detects modifications to exported Go symbols that could
// break callers in other files or packages. Returns a guidance string if
// potentially-breaking changes are found.
func checkBreakingChanges(filePath, oldContent, newContent string) string {
	if !strings.HasSuffix(filePath, ".go") {
		return ""
	}

	oldDecls := parseExportedDecls(oldContent)
	newDecls := parseExportedDecls(newContent)

	if oldDecls.isEmpty() || newDecls.isEmpty() {
		return ""
	}

	var warnings []string

	// Check exported functions and methods.
	for key, oldSig := range oldDecls.funcs {
		if newSig, ok := newDecls.funcs[key]; ok {
			if oldSig != newSig {
				warnings = append(warnings,
					fmt.Sprintf("Exported %s signature changed (was: %s -> now: %s). "+
						"All callers must be updated - search for usages with grep or lsp_references.",
						key, oldSig, newSig))
			}
		}
	}

	// Check exported type definitions (structs, interfaces, aliases).
	for key := range oldDecls.types {
		if newSig, ok := newDecls.types[key]; ok {
			if oldDecls.types[key] != newSig {
				warnings = append(warnings,
					fmt.Sprintf("Exported type %s definition changed. "+
						"All code depending on this type (initialization, type assertions, interface implementations) must be reviewed.",
						key))
			}
		}
	}

	// Check exported vars and consts.
	for key, oldSig := range oldDecls.values {
		if newSig, ok := newDecls.values[key]; ok {
			if oldSig != newSig {
				warnings = append(warnings,
					fmt.Sprintf("Exported value %s changed type or category. "+
						"Verify all usages still compile.", key))
			}
		}
	}

	if len(warnings) == 0 {
		return ""
	}

	// Limit output.
	if len(warnings) > 2 {
		moreCount := len(warnings) - 2 // compute before truncation
		warnings = warnings[:2]
		warnings = append(warnings, fmt.Sprintf("...and %d more breaking change(s)", moreCount))
	}

	var b strings.Builder
	b.WriteString("Breaking change to exported symbol(s) detected:\n")
	for _, w := range warnings {
		b.WriteString("  - ")
		b.WriteString(w)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// exportedDeclSet holds signatures of exported declarations parsed from a Go file.
type exportedDeclSet struct {
	funcs  map[string]string // "FuncName" or "RecvType.MethodName" -> signature string
	types  map[string]string // "TypeName" -> structural fingerprint
	values map[string]string // "VarName" or "ConstName" -> type string
}

func (s *exportedDeclSet) isEmpty() bool {
	return len(s.funcs) == 0 && len(s.types) == 0 && len(s.values) == 0
}

// parseExportedDecls parses Go source and extracts signatures of exported
// declarations. Returns empty set on parse failure.
func parseExportedDecls(src string) *exportedDeclSet {
	result := &exportedDeclSet{
		funcs:  make(map[string]string),
		types:  make(map[string]string),
		values: make(map[string]string),
	}

	if strings.TrimSpace(src) == "" {
		return result
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return result
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if !ast.IsExported(name) {
				continue
			}
			key := name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvType := breakingRecvType(d.Recv.List[0].Type)
				if recvType == "" {
					continue
				}
				key = recvType + "." + name
				// Skip methods on non-exported types.
				if !ast.IsExported(recvType) {
					continue
				}
			}
			result.funcs[key] = breakingFuncSig(d.Type)

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if !ast.IsExported(s.Name.Name) {
						continue
					}
					result.types[s.Name.Name] = breakingTypeFP(s.Type)

				case *ast.ValueSpec:
					for _, name := range s.Names {
						if !ast.IsExported(name.Name) {
							continue
						}
						result.values[name.Name] = breakingValueFP(s, d.Tok)
					}
				}
			}
		}
	}

	return result
}

// breakingFuncSig produces a stable string representation of a function type
// signature. It captures parameter types and return types but not parameter
// names (which can change without breaking callers).
func breakingFuncSig(ft *ast.FuncType) string {
	var b strings.Builder
	b.WriteString("func(")
	if ft.Params != nil {
		for i, param := range ft.Params.List {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(breakingExprStr(param.Type))
		}
	}
	b.WriteString(")")

	if ft.Results != nil && len(ft.Results.List) > 0 {
		b.WriteString(" (")
		for i, result := range ft.Results.List {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(breakingExprStr(result.Type))
		}
		b.WriteString(")")
	}

	return b.String()
}

// breakingTypeFP produces a structural fingerprint of a type declaration.
func breakingTypeFP(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StructType:
		if t.Fields == nil {
			return "struct{}"
		}
		var fields []string
		for _, field := range t.Fields.List {
			if len(field.Names) == 0 {
				fields = append(fields, "embed:"+breakingExprStr(field.Type))
			} else {
				ftype := breakingExprStr(field.Type)
				for _, name := range field.Names {
					fields = append(fields, name.Name+":"+ftype)
				}
			}
		}
		return "struct{" + strings.Join(fields, "; ") + "}"

	case *ast.InterfaceType:
		if t.Methods == nil {
			return "interface{}"
		}
		var methods []string
		for _, method := range t.Methods.List {
			if len(method.Names) == 0 {
				methods = append(methods, "embed:"+breakingExprStr(method.Type))
			} else {
				for _, name := range method.Names {
					methods = append(methods, name.Name)
				}
			}
		}
		return "interface{" + strings.Join(methods, "; ") + "}"

	case *ast.Ident:
		return "alias=" + t.Name

	default:
		return "other=" + breakingExprStr(expr)
	}
}

// breakingValueFP produces a signature for a var/const declaration.
func breakingValueFP(spec *ast.ValueSpec, tok token.Token) string {
	var b strings.Builder
	b.WriteString(tok.String())
	if spec.Type != nil {
		b.WriteString(":")
		b.WriteString(breakingExprStr(spec.Type))
	}
	return b.String()
}

// breakingRecvType extracts the type name from a receiver type expression,
// handling pointer receivers (*Type -> Type).
func breakingRecvType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// breakingExprStr converts an AST expression to a compact string representation.
func breakingExprStr(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + breakingExprStr(t.X)
	case *ast.SelectorExpr:
		return breakingExprStr(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + breakingExprStr(t.Elt)
		}
		return "[" + breakingExprStr(t.Len) + "]" + breakingExprStr(t.Elt)
	case *ast.MapType:
		return "map[" + breakingExprStr(t.Key) + "]" + breakingExprStr(t.Value)
	case *ast.ChanType:
		return "chan " + breakingExprStr(t.Value)
	case *ast.FuncType:
		return breakingFuncSig(t)
	case *ast.StructType:
		return breakingTypeFP(t)
	case *ast.InterfaceType:
		return breakingTypeFP(t)
	case *ast.Ellipsis:
		return "..." + breakingExprStr(t.Elt)
	case *ast.BasicLit:
		return t.Value
	default:
		return "expr"
	}
}
