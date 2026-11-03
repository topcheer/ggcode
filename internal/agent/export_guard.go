package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type exportGuardState struct {
	checked map[string]bool
}

func newExportGuardState() *exportGuardState {
	return &exportGuardState{
		checked: make(map[string]bool),
	}
}

func (s *exportGuardState) reset() {
	s.checked = make(map[string]bool)
}

type exportSymbol struct {
	Name      string
	Kind      string
	Signature string
}

type breakingChange struct {
	Symbol string
	Kind   string
	Detail string
}

func (a *Agent) checkExportGuard(filePath string) string {
	if a.exportGuard == nil || filePath == "" {
		return ""
	}

	if !strings.HasSuffix(filePath, ".go") || strings.HasSuffix(filePath, "_test.go") {
		return ""
	}

	abs := filePath
	if !filepath.IsAbs(abs) && a.workingDir != "" {
		abs = filepath.Join(a.workingDir, filePath)
	}
	if a.exportGuard.checked[abs] {
		return ""
	}

	oldSyms := gitHeadExportSymbols(a.workingDir, filePath)
	if oldSyms == nil {

		return ""
	}

	newSyms := parseExportedSymbols(abs)
	if newSyms == nil {

		return ""
	}

	a.exportGuard.checked[abs] = true

	changes := diffExportSymbols(oldSyms, newSyms)
	if len(changes) == 0 {

		delete(a.exportGuard.checked, abs)
		return ""
	}

	debug.Log("export-guard", "detected %d breaking change(s) in %s", len(changes), filepath.Base(filePath))
	return formatBreakingChangeWarning(filePath, changes)
}

func gitHeadExportSymbols(workingDir, filePath string) []exportSymbol {

	content, err := gitFileContentAtHEAD(workingDir, filePath)
	if err != nil {
		debug.Log("export-guard", "git show HEAD:%s failed: %v", filePath, err)
		return nil
	}
	return parseExportedSymbolsFromSource([]byte(content))
}

func parseExportedSymbols(filePath string) []exportSymbol {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	file, _, err := parseGoSource(filePath, string(data), 0)
	if err != nil {
		return nil
	}
	return extractExportedSymbols(file)
}

func parseExportedSymbolsFromSource(src []byte) []exportSymbol {
	file, _, err := parseGoSource("", string(src), 0)
	if err != nil {
		return nil
	}
	return extractExportedSymbols(file)
}

func extractExportedSymbols(file *ast.File) []exportSymbol {
	var syms []exportSymbol

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil || !d.Name.IsExported() {
				continue
			}
			name := d.Name.Name
			if strings.HasPrefix(name, "Test") ||
				strings.HasPrefix(name, "Benchmark") ||
				strings.HasPrefix(name, "Example") ||
				strings.HasPrefix(name, "Fuzz") {
				continue
			}

			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvType := receiverTypeName(d.Recv.List[0].Type)
				if recvType != "" {
					sig := funcSignatureWithReceiver(d.Recv, d.Type)
					syms = append(syms, exportSymbol{
						Name:      recvType + "." + name,
						Kind:      "method",
						Signature: sig,
					})
				}
			} else {
				sig := normalizeFuncSignature(d.Type)
				syms = append(syms, exportSymbol{
					Name:      name,
					Kind:      "func",
					Signature: sig,
				})
			}

		case *ast.GenDecl:

			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name != nil && s.Name.IsExported() {
						kind := "type"
						signature := ""
						if ifaceType, ok := s.Type.(*ast.InterfaceType); ok {
							kind = "interface"

							signature = extractInterfaceMethodFingerprint(ifaceType)
						} else if structType, ok := s.Type.(*ast.StructType); ok {
							kind = "struct"

							signature = extractStructFieldFingerprint(structType)
						}
						syms = append(syms, exportSymbol{
							Name:      s.Name.Name,
							Kind:      kind,
							Signature: signature,
						})
					}
				case *ast.ValueSpec:

					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, name := range s.Names {
						if name.IsExported() {
							sig := ""
							if s.Type != nil {
								sig = normalizeType(s.Type)
							}
							syms = append(syms, exportSymbol{
								Name:      name.Name,
								Kind:      kind,
								Signature: sig,
							})
						}
					}
				}
			}
		}
	}

	return syms
}

func normalizeFuncSignature(ft *ast.FuncType) string {

	typeParams := ""
	if ft.TypeParams != nil && len(ft.TypeParams.List) > 0 {
		typeParams = normalizeFieldList(ft.TypeParams)
	}

	params := normalizeFieldList(ft.Params)
	results := ""
	if ft.Results != nil {
		results = normalizeFieldList(ft.Results)
	}
	return fmt.Sprintf("<%s>(%s)(%s)", typeParams, params, results)
}

func funcSignatureWithReceiver(recv *ast.FieldList, ft *ast.FuncType) string {
	recvPrefix := ""
	if recv != nil && len(recv.List) > 0 {
		recvType := normalizeType(recv.List[0].Type)
		if _, ok := recv.List[0].Type.(*ast.StarExpr); ok {
			recvPrefix = "ptr-recv:" + recvType
		} else {
			recvPrefix = "val-recv:" + recvType
		}
	}
	sig := normalizeFuncSignature(ft)
	if recvPrefix != "" {
		return recvPrefix + ":" + sig
	}
	return sig
}

func normalizeType(expr ast.Expr) string {

	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		if xIdent, ok := selector.X.(*ast.Ident); ok {
			return xIdent.Name + "." + selector.Sel.Name
		}
	}

	if star, ok := expr.(*ast.StarExpr); ok {
		return "*" + normalizeType(star.X)
	}

	if array, ok := expr.(*ast.ArrayType); ok {
		if array.Len == nil {
			return "[]" + normalizeType(array.Elt)
		}

		return "[" + exprString(array.Len) + "]" + normalizeType(array.Elt)
	}

	if mp, ok := expr.(*ast.MapType); ok {
		return "map[" + normalizeType(mp.Key) + "]" + normalizeType(mp.Value)
	}

	if chanType, ok := expr.(*ast.ChanType); ok {
		prefix := "chan "
		if chanType.Dir == ast.SEND {
			prefix = "chan<- "
		} else if chanType.Dir == ast.RECV {
			prefix = "<-chan "
		}
		return prefix + normalizeType(chanType.Value)
	}

	if fnType, ok := expr.(*ast.FuncType); ok {
		return "func" + normalizeFuncSignature(fnType)
	}

	if iface, ok := expr.(*ast.InterfaceType); ok {
		fingerprint := extractInterfaceMethodFingerprint(iface)
		if fingerprint != "" {
			return "interface{" + fingerprint + "}"
		}
		return "interface{}"
	}

	if structType, ok := expr.(*ast.StructType); ok {
		fingerprint := extractStructFieldFingerprint(structType)
		if fingerprint != "" {
			return "struct{" + fingerprint + "}"
		}
		return "struct{}"
	}
	return "unknown"
}

func extractInterfaceMethodFingerprint(ifaceType *ast.InterfaceType) string {
	if ifaceType.Methods == nil {
		return ""
	}

	var methods []string
	for _, field := range ifaceType.Methods.List {
		if len(field.Names) > 0 {

			if ft, ok := field.Type.(*ast.FuncType); ok {
				for _, name := range field.Names {

					sig := normalizeFuncSignature(ft)
					methods = append(methods, name.Name+":"+sig)
				}
			}
		} else {

			if embeddedType := normalizeType(field.Type); embeddedType != "" {
				methods = append(methods, "embed:"+embeddedType)
			}
		}
	}
	sort.Strings(methods)
	return strings.Join(methods, "|")
}

func extractStructFieldFingerprint(structType *ast.StructType) string {
	if structType.Fields == nil {
		return ""
	}

	var fields []string
	for _, field := range structType.Fields.List {

		if len(field.Names) > 0 {

			for _, name := range field.Names {
				if name.IsExported() {
					ty := normalizeType(field.Type)
					fields = append(fields, name.Name+":"+ty)
				}
			}
		} else {

			ty := normalizeType(field.Type)
			if ty != "" {
				fields = append(fields, "embed:"+ty)
			}
		}
	}
	sort.Strings(fields)
	return strings.Join(fields, "|")
}

func normalizeFieldList(fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, field := range fl.List {
		typeStr := exprString(field.Type)
		n := len(field.Names)
		if n == 0 {

			parts = append(parts, typeStr)
		} else {
			for range field.Names {
				parts = append(parts, typeStr)
			}
		}
	}
	return strings.Join(parts, ",")
}

func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprString(e.X)
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.ArrayType:
		if e.Len == nil {

			return "[]" + exprString(e.Elt)
		}

		return "[" + exprString(e.Len) + "]" + exprString(e.Elt)
	case *ast.MapType:
		return "map[" + exprString(e.Key) + "]" + exprString(e.Value)
	case *ast.ChanType:

		prefix := "chan "
		if e.Dir == ast.SEND {
			prefix = "chan<- "
		} else if e.Dir == ast.RECV {
			prefix = "<-chan "
		}
		return prefix + exprString(e.Value)
	case *ast.FuncType:
		return "func" + normalizeFuncSignature(e)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.StructType:
		return "struct{}"
	case *ast.Ellipsis:
		return "..." + exprString(e.Elt)
	case *ast.BasicLit:

		return e.Value
	default:
		return "expr"
	}
}

func diffExportSymbols(old, new []exportSymbol) []breakingChange {
	oldMap := make(map[string]exportSymbol, len(old))
	for _, s := range old {
		oldMap[s.Name+"|"+s.Kind] = s
	}
	newMap := make(map[string]exportSymbol, len(new))
	for _, s := range new {
		newMap[s.Name+"|"+s.Kind] = s
	}

	var changes []breakingChange

	for key, oldSym := range oldMap {
		newSym, exists := newMap[key]
		if !exists {
			changes = append(changes, breakingChange{
				Symbol: oldSym.Name,
				Kind:   "removed",
				Detail: oldSym.Kind,
			})
		} else if oldSym.Signature != newSym.Signature && (oldSym.Signature != "" || newSym.Signature != "") {

			changes = append(changes, breakingChange{
				Symbol: oldSym.Name,
				Kind:   "signature-changed",
				Detail: fmt.Sprintf("%s → %s", oldSym.Signature, newSym.Signature),
			})
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Symbol != changes[j].Symbol {
			return changes[i].Symbol < changes[j].Symbol
		}
		return changes[i].Kind < changes[j].Kind
	})

	return changes
}

func formatBreakingChangeWarning(filePath string, changes []breakingChange) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[Breaking Change Warning] Edits to %s modified or removed %d exported symbol(s):\n",
		filepath.Base(filePath), len(changes)))

	maxShow := 8
	for i, c := range changes {
		if i >= maxShow {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(changes)-maxShow))
			break
		}
		switch c.Kind {
		case "removed":
			b.WriteString(fmt.Sprintf("  REMOVED: %s (%s)\n", c.Symbol, c.Detail))
		case "signature-changed":
			b.WriteString(fmt.Sprintf("  CHANGED: %s signature %s\n", c.Symbol, c.Detail))
		default:
			b.WriteString(fmt.Sprintf("  %s: %s\n", c.Kind, c.Symbol))
		}
	}

	b.WriteString("\nThese symbols may be used by other packages. Search for references")
	b.WriteString(" (grep, lsp_references) and update all callers before proceeding.")
	b.WriteString(" If this was an intentional rename, update all call sites.")
	return b.String()
}
