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
	a.exportGuard.checked[abs] = true

	oldSyms := gitHeadExportSymbols(a.workingDir, filePath)
	if oldSyms == nil {
		// File not tracked in git or parse error in HEAD version — can't compare.
		return ""
	}

	newSyms := parseExportedSymbols(abs)
	if newSyms == nil {
		// Current file unparseable — other guards will catch syntax errors.
		return ""
	}

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
			sig := normalizeFuncSignature(d.Type)
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvType := receiverTypeName(d.Recv.List[0].Type)
				if recvType != "" {
					syms = append(syms, exportSymbol{
						Name:      recvType + "." + name,
						Kind:      "method",
						Signature: sig,
					})
				}
			} else {
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
						switch s.Type.(type) {
						case *ast.InterfaceType:
							kind = "interface"
						case *ast.StructType:
							kind = "struct"
						}
						syms = append(syms, exportSymbol{
							Name: s.Name.Name,
							Kind: kind,
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
// (parameter and result types) so that signature changes can be detected.
// e.g., func Foo(a int, b string) error → "(int,string)(error)"
func normalizeFuncSignature(ft *ast.FuncType) string {
	params := normalizeFieldList(ft.Params)
	results := ""
	if ft.Results != nil {
		results = normalizeFieldList(ft.Results)
	}
	return fmt.Sprintf("(%s)(%s)", params, results)
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
