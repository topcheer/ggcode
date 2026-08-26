package agent

// export_guard.go implements Export/Signature Change Detection — a deterministic
// post-edit guard that warns when edits to Go files remove or change exported
// symbols (functions, methods, types, interfaces, constants, variables).
//
// This catches the #1 source of downstream compilation errors in agent-generated
// code: the agent renames, removes, or rewrites an exported function's signature
// without realizing that other packages depend on it.
//
// Research basis: "Regression Guard" pattern (Agensi, Exceeds AI 2026) —
// "behavioral contract checks caught a breaking signature change that both
// tsc and vite build silently passed." Our implementation uses Go AST parsing
// and git HEAD comparison instead of behavioral contracts, making it zero-cost
// and applicable to any Go codebase.
//
// Competitor mapping:
//   - GitHub Copilot: no runtime breaking-change detection during edits
//   - Cursor: warns about unused imports but not removed exports
//   - Claude Code: relies on post-build compiler errors (too late)
//   - OpenHands: no export-level change analysis
//
// Our approach:
//  1. After a successful edit to a .go file, read the file's content from git
//     HEAD (the last committed version).
//  2. Parse exported symbols from both HEAD and current versions using go/ast.
//  3. Diff: report removed exports and changed function signatures.
//  4. Inject a targeted warning listing the specific breaking changes and
//     advising the agent to search for and update downstream callers.
//
// The guard fires at most once per file per run to avoid noise on iterative
// edits. Parsing is microseconds-fast (single file, stdlib only).

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// exportGuardState tracks which files have already been checked to avoid
// repeating the same warning on iterative edits.
type exportGuardState struct {
	checked map[string]bool // file paths already analyzed this run
}

func newExportGuardState() *exportGuardState {
	return &exportGuardState{
		checked: make(map[string]bool),
	}
}

func (s *exportGuardState) reset() {
	s.checked = make(map[string]bool)
}

// exportSymbol represents a single exported symbol with enough information to
// detect removals and signature changes.
type exportSymbol struct {
	Name      string // e.g., "Foo" or "Bar_Method" (methods prefixed with receiver type)
	Kind      string // "func", "method", "type", "const", "var"
	Signature string // normalized signature for func/method (param+return types), empty for others
}

// breakingChange represents a detected breaking change.
type breakingChange struct {
	Symbol string
	Kind   string // "removed", "signature-changed"
	Detail string // old → new signature for signature-changed
}

// checkExportGuard runs breaking-change analysis on a Go file after a successful
// edit. Returns a guidance string if breaking changes are found, "" otherwise.
func (a *Agent) checkExportGuard(filePath string) string {
	if a.exportGuard == nil || filePath == "" {
		return ""
	}

	// Only check Go source files (not test files — test exports are internal).
	if !strings.HasSuffix(filePath, ".go") || strings.HasSuffix(filePath, "_test.go") {
		return ""
	}

	// Fire at most once per file per run.
	abs := filePath
	if !filepath.IsAbs(abs) && a.workingDir != "" {
		abs = filepath.Join(a.workingDir, filePath)
	}
	if a.exportGuard.checked[abs] {
		return ""
	}

	oldSyms := gitHeadExportSymbols(a.workingDir, filePath)
	if oldSyms == nil {
		// File not tracked in git or parse error in HEAD version — can't
		// compare. Do NOT burn the once-per-run marker here (#502): a mid-run
		// commit can make the comparison possible for later edits of this
		// file, and the burned marker kept the guard silent for exactly the
		// breaking changes it exists to catch.
		return ""
	}

	newSyms := parseExportedSymbols(abs)
	if newSyms == nil {
		// Current file unparseable — other guards will catch syntax errors.
		// Do NOT burn the marker here (#1043): if parsing fails now but
		// succeeds later (after a syntax fix), we want to check again.
		return ""
	}
	// Issue #1043(a): Only burn the once-per-run marker after successful
	// parsing. If parsing fails, the guard should retry on subsequent edits.
	a.exportGuard.checked[abs] = true

	changes := diffExportSymbols(oldSyms, newSyms)
	if len(changes) == 0 {
		return ""
	}

	debug.Log("export-guard", "detected %d breaking change(s) in %s", len(changes), filepath.Base(filePath))
	return formatBreakingChangeWarning(filePath, changes)
}

// gitHeadExportSymbols reads a Go file from git HEAD and returns its exported
// symbols. Returns nil if the file isn't tracked in git or can't be parsed.
func gitHeadExportSymbols(workingDir, filePath string) []exportSymbol {
	relPath := filePath
	if filepath.IsAbs(relPath) && workingDir != "" {
		if rel, err := filepath.Rel(workingDir, filePath); err == nil {
			relPath = rel
		}
	}

	cmd := exec.Command("git", "show", "HEAD:"+relPath)
	cmd.Dir = workingDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		debug.Log("export-guard", "git show HEAD:%s failed: %v", relPath, err)
		return nil
	}

	return parseExportedSymbolsFromSource(stdout.Bytes())
}

// parseExportedSymbols reads a Go source file from disk and returns its
// exported symbols. Returns nil on parse error.
func parseExportedSymbols(filePath string) []exportSymbol {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		return nil
	}
	return extractExportedSymbols(file)
}

// parseExportedSymbolsFromSource parses Go source from a byte slice and returns
// its exported symbols.
func parseExportedSymbolsFromSource(src []byte) []exportSymbol {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}
	return extractExportedSymbols(file)
}

// extractExportedSymbols scans an AST file and collects all exported symbols
// with their signatures. Skips init, main, and test-prefixed functions.
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
				continue // test helpers
			}

			// Issue #1043(c): use funcSignatureWithReceiver for methods
			// to include receiver type (value vs pointer) in the fingerprint.
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
			// Handle type, const, var declarations.
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name != nil && s.Name.IsExported() {
						kind := "type"
						signature := ""
						if ifaceType, ok := s.Type.(*ast.InterfaceType); ok {
							kind = "interface"
							// Bug B fix: record method set fingerprint for interfaces
							signature = extractInterfaceMethodFingerprint(ifaceType)
						} else if structType, ok := s.Type.(*ast.StructType); ok {
							kind = "struct"
							// Issue #1043(b): record field set fingerprint for structs
							signature = extractStructFieldFingerprint(structType)
						}
						syms = append(syms, exportSymbol{
							Name:      s.Name.Name,
							Kind:      kind,
							Signature: signature,
						})
					}
				case *ast.ValueSpec:
					// const or var
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, name := range s.Names {
						if name.IsExported() {
							syms = append(syms, exportSymbol{
								Name: name.Name,
								Kind: kind,
							})
						}
					}
				}
			}
		}
	}

	return syms
}

// normalizeFuncSignature creates a string fingerprint of a function's signature
// (type parameters, receiver, parameter and result types) so that signature
// changes can be detected.
// e.g., func Foo(a int, b string) error → "(int,string)(error)"
// With receiver: func (r *MyType) Foo() error → "ptr-recv:MyType()()()"
func normalizeFuncSignature(ft *ast.FuncType) string {
	// Issue #1043(c): include type parameters
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

// funcSignatureWithReceiver creates a fingerprint that includes receiver type.
// Used for method signatures to distinguish between value and pointer receivers.
// Issue #1043(c): value vs pointer receivers must be different.
func funcSignatureWithReceiver(recv *ast.FieldList, ft *ast.FuncType) string {
	recvPrefix := ""
	if recv != nil && len(recv.List) > 0 && len(recv.List[0].Names) > 0 {
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

// normalizeType converts an AST type expression to a normalized string representation.
// Used for struct fields and receiver types in fingerprint generation.
func normalizeType(expr ast.Expr) string {
	// Handle basic types and qualified identifiers
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		if xIdent, ok := selector.X.(*ast.Ident); ok {
			return xIdent.Name + "." + selector.Sel.Name
		}
	}
	// Handle pointer types: strip the * prefix
	if star, ok := expr.(*ast.StarExpr); ok {
		return "*" + normalizeType(star.X)
	}
	// Handle slice types
	if array, ok := expr.(*ast.ArrayType); ok {
		return "[]" + normalizeType(array.Elt)
	}
	// Handle map types
	if mp, ok := expr.(*ast.MapType); ok {
		return "map[" + normalizeType(mp.Key) + "]" + normalizeType(mp.Value)
	}
	// For complex types (chan, func, struct, interface), return a simplified placeholder
	// TODO(#1043): Expand support for full type fidelity if needed.
	return "complex"
}

// extractInterfaceMethodFingerprint creates a sorted string fingerprint of all
// method names and signatures in an interface. Used by Bug B fix to detect
// when methods are added or removed from exported interfaces.
// Issue #1043(c): now uses funcSignatureWithReceiver to include receiver info.
func extractInterfaceMethodFingerprint(ifaceType *ast.InterfaceType) string {
	if ifaceType.Methods == nil {
		return ""
	}

	var methods []string
	for _, field := range ifaceType.Methods.List {
		if ft, ok := field.Type.(*ast.FuncType); ok && len(field.Names) > 0 {
			for _, name := range field.Names {
				// Issue #1043(c): interface methods have no receiver field,
				// but we still use the unified signature for consistency
				sig := normalizeFuncSignature(ft)
				methods = append(methods, name.Name+":"+sig)
			}
		}
	}
	sort.Strings(methods)
	return strings.Join(methods, "|")
}

// extractStructFieldFingerprint creates a sorted string fingerprint of all
// exported field names and types in a struct. Used by Issue #1043(b) to detect
// when fields are added, removed, or renamed from exported structs.
// TODO(#1043): Handle embedded struct/interface fields (anonymous fields).
func extractStructFieldFingerprint(structType *ast.StructType) string {
	if structType.Fields == nil {
		return ""
	}

	var fields []string
	for _, field := range structType.Fields.List {
		// Process named fields (skip anonymous/embedded fields for now)
		for _, name := range field.Names {
			if name.IsExported() {
				ty := normalizeType(field.Type)
				fields = append(fields, name.Name+":"+ty)
			}
		}
	}
	sort.Strings(fields)
	return strings.Join(fields, "|")
}

// normalizeFieldList extracts a comma-separated type string from an AST
// FieldList (used for params and returns).
func normalizeFieldList(fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, field := range fl.List {
		typeStr := exprString(field.Type)
		n := len(field.Names)
		if n == 0 {
			// unnamed parameter/result
			parts = append(parts, typeStr)
		} else {
			for range field.Names {
				parts = append(parts, typeStr)
			}
		}
	}
	return strings.Join(parts, ",")
}

// exprString converts an AST expression to a simplified type string.
func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprString(e.X)
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprString(e.Elt)
	case *ast.MapType:
		return "map[" + exprString(e.Key) + "]" + exprString(e.Value)
	case *ast.ChanType:
		return "chan " + exprString(e.Value)
	case *ast.FuncType:
		return "func" + normalizeFuncSignature(e)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.StructType:
		return "struct{}"
	case *ast.Ellipsis:
		return "..." + exprString(e.Elt)
	default:
		return "expr"
	}
}

// diffExportSymbols compares old and new symbol sets, returning breaking changes.
// A change is "breaking" if:
//   - A symbol exists in old but not in new (removed).
//   - A func/method has the same name but a different signature (signature-changed).
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

	// Check for removed or changed symbols.
	for key, oldSym := range oldMap {
		newSym, exists := newMap[key]
		if !exists {
			changes = append(changes, breakingChange{
				Symbol: oldSym.Name,
				Kind:   "removed",
				Detail: oldSym.Kind,
			})
		} else if oldSym.Signature != "" && newSym.Signature != "" && oldSym.Signature != newSym.Signature {
			changes = append(changes, breakingChange{
				Symbol: oldSym.Name,
				Kind:   "signature-changed",
				Detail: fmt.Sprintf("%s → %s", oldSym.Signature, newSym.Signature),
			})
		}
	}

	// Sort for deterministic output.
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Symbol != changes[j].Symbol {
			return changes[i].Symbol < changes[j].Symbol
		}
		return changes[i].Kind < changes[j].Kind
	})

	return changes
}

// formatBreakingChangeWarning produces the guidance string injected into the
// tool result.
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
