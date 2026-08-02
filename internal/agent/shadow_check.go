package agent

// Variable shadowing detection for Go files.
//
// Problem: AI coding agents frequently introduce variable shadowing bugs,
// especially with error variables. The classic Go pitfall:
//
//	func process() error {
//	    err := db.Save(record)
//	    if err != nil {
//	        return err
//	    }
//	    if cond {
//	        err := validate(record) // SHADOWS outer err!
//	        // This err is a NEW variable. Outer err stays nil.
//	    }
//	    return nil // outer err unchanged even if validate failed
//	}
//
// Shadowing of error variables (err) is particularly dangerous because the
// inner err is a NEW variable, not a reassignment. Errors are silently
// swallowed when the inner scope exits. go vet does not flag this pattern
// and staticcheck only flags loop variable shadowing.
//
// Competitor analysis:
//   - Claude Code: no inline detection
//   - Cursor: lint-on-save may catch some via gopls, but not inline post-edit
//   - Cline/OpenHands: reactive only
//   - Aider: no detection
//   - GitHub Copilot: no post-edit shadowing analysis
//
// Approach: AST-based analysis. For each function, we track the set of
// variables declared at the function level (params, receiver, and top-level
// := / var / const declarations in the function body). Then we walk nested
// scopes (if/for/range/switch/select blocks) and detect := assignments
// that redeclare a name already visible from the enclosing scope.
//
// Delta-aware: only flags NEW shadowing introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// shadowInfo records a single variable shadowing occurrence.
type shadowInfo struct {
	varName string
	line    int
	isError bool
}

// checkVarShadowing detects Go variable shadowing where a := in an inner
// scope creates a new variable hiding an outer-scope variable of the same
// name. Returns warning strings. Only flags NEW occurrences (delta-aware).
func checkVarShadowing(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	oldIssues := findVarShadowing(filePath, oldContent)
	newIssues := findVarShadowing(filePath, newContent)

	// Delta: compare by (varName, isError) since line numbers shift.
	type shadowSig struct {
		name string
		err  bool
	}
	oldSet := make(map[shadowSig]bool)
	for _, o := range oldIssues {
		oldSet[shadowSig{o.varName, o.isError}] = true
	}

	var newShadows []shadowInfo
	for _, n := range newIssues {
		if !oldSet[shadowSig{n.varName, n.isError}] {
			newShadows = append(newShadows, n)
		}
	}

	if len(newShadows) == 0 {
		return nil
	}

	var errNames, otherNames []string
	for _, s := range newShadows {
		if s.isError {
			errNames = append(errNames, s.varName)
		} else {
			otherNames = append(otherNames, s.varName)
		}
	}

	var parts []string
	if len(errNames) > 0 {
		parts = append(parts, fmt.Sprintf(
			"Error variable shadowing detected: %s. "+
				"Using := in an inner scope creates a NEW variable, hiding the outer one. "+
				"The outer error variable remains unchanged, so errors from the inner scope are silently lost. "+
				"Use = (assignment) instead of := to reuse the outer variable, or use a distinct name.",
			strings.Join(errNames, ", ")))
	}
	if len(otherNames) > 0 {
		parts = append(parts, fmt.Sprintf(
			"Variable shadowing detected: %s. "+
				"An inner-scope := declaration hides an outer variable of the same name, "+
				"which can cause confusion and subtle bugs. Use a distinct name to avoid ambiguity.",
			strings.Join(otherNames, ", ")))
	}

	return parts
}

// findVarShadowing parses Go source and returns all shadowing occurrences.
func findVarShadowing(filename, src string) []shadowInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil
	}

	var results []shadowInfo
	seen := make(map[string]bool)

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Collect names visible in the function's top-level scope.
		topLevel := make(map[string]bool)
		if fn.Recv != nil {
			collectNamesFromFieldList(fn.Recv, topLevel)
		}
		collectNamesFromFieldList(fn.Type.Params, topLevel)
		if fn.Type.Results != nil {
			collectNamesFromFieldList(fn.Type.Results, topLevel)
		}

		// Collect top-level declarations from the function body (non-nested).
		for _, stmt := range fn.Body.List {
			collectTopLevelNames(stmt, topLevel)
		}

		// Walk nested scopes to find shadowing.
		for _, stmt := range fn.Body.List {
			findShadowInStmt(stmt, topLevel, fset, &results, seen)
		}
	}

	return results
}

// collectTopLevelNames adds variable names from a statement that introduces
// them at the current scope level (not inside nested blocks).
func collectTopLevelNames(stmt ast.Stmt, m map[string]bool) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if s.Tok == token.DEFINE {
			for _, lhs := range s.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
					m[ident.Name] = true
				}
			}
		}
	case *ast.DeclStmt:
		if gd, ok := s.Decl.(*ast.GenDecl); ok {
			for _, spec := range gd.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range vs.Names {
						if name.Name != "_" {
							m[name.Name] = true
						}
					}
				}
			}
		}
	case *ast.RangeStmt:
		// Range with := at top level declares loop vars.
		if s.Key != nil {
			if ident, ok := s.Key.(*ast.Ident); ok && ident.Name != "_" {
				m[ident.Name] = true
			}
		}
		if s.Value != nil {
			if ident, ok := s.Value.(*ast.Ident); ok && ident.Name != "_" {
				m[ident.Name] = true
			}
		}
	case *ast.LabeledStmt:
		if s.Stmt != nil {
			collectTopLevelNames(s.Stmt, m)
		}
	}
}

// findShadowInStmt recursively walks statements looking for shadowing in
// nested scopes. outerVars is the set of names visible from enclosing scopes.
func findShadowInStmt(stmt ast.Stmt, outerVars map[string]bool,
	fset *token.FileSet, results *[]shadowInfo, seen map[string]bool) {

	switch s := stmt.(type) {
	// --- Scope-creating statements ---

	case *ast.IfStmt:
		// Init creates a new scope visible to Cond and Body.
		scopedVars := copyMap(outerVars)
		if s.Init != nil {
			collectDeclNamesStmt(s.Init, scopedVars)
			findShadowInStmt(s.Init, scopedVars, fset, results, seen)
		}
		if s.Body != nil {
			findShadowInBlock(s.Body, scopedVars, fset, results, seen)
		}
		if s.Else != nil {
			findShadowInStmt(s.Else, outerVars, fset, results, seen)
		}

	case *ast.ForStmt:
		scopedVars := copyMap(outerVars)
		if s.Init != nil {
			collectDeclNamesStmt(s.Init, scopedVars)
		}
		if s.Body != nil {
			findShadowInBlock(s.Body, scopedVars, fset, results, seen)
		}

	case *ast.RangeStmt:
		scopedVars := copyMap(outerVars)
		// Range key/value with := create new variables in the loop scope.
		if s.Key != nil {
			if ident, ok := s.Key.(*ast.Ident); ok && ident.Name != "_" && s.Tok == token.DEFINE {
				if outerVars[ident.Name] {
					recordShadow(ident, fset, results, seen)
				}
				scopedVars[ident.Name] = true
			}
		}
		if s.Value != nil {
			if ident, ok := s.Value.(*ast.Ident); ok && ident.Name != "_" && s.Tok == token.DEFINE {
				if outerVars[ident.Name] {
					recordShadow(ident, fset, results, seen)
				}
				scopedVars[ident.Name] = true
			}
		}
		if s.Body != nil {
			findShadowInBlock(s.Body, scopedVars, fset, results, seen)
		}

	case *ast.SwitchStmt:
		scopedVars := copyMap(outerVars)
		if s.Init != nil {
			collectDeclNamesStmt(s.Init, scopedVars)
			findShadowInStmt(s.Init, scopedVars, fset, results, seen)
		}
		if s.Body != nil {
			for _, c := range s.Body.List {
				if clause, ok := c.(*ast.CaseClause); ok {
					clauseVars := copyMap(scopedVars)
					findShadowInBlock(&ast.BlockStmt{List: clause.Body}, clauseVars, fset, results, seen)
				}
			}
		}

	case *ast.TypeSwitchStmt:
		scopedVars := copyMap(outerVars)
		if s.Init != nil {
			collectDeclNamesStmt(s.Init, scopedVars)
		}
		if s.Assign != nil {
			collectDeclNamesStmt(s.Assign, scopedVars)
		}
		if s.Body != nil {
			for _, c := range s.Body.List {
				if clause, ok := c.(*ast.CaseClause); ok {
					clauseVars := copyMap(scopedVars)
					findShadowInBlock(&ast.BlockStmt{List: clause.Body}, clauseVars, fset, results, seen)
				}
			}
		}

	case *ast.SelectStmt:
		if s.Body != nil {
			for _, comm := range s.Body.List {
				clause, ok := comm.(*ast.CommClause)
				if !ok {
					continue
				}
				scopedVars := copyMap(outerVars)
				if clause.Comm != nil {
					// Check shadowing in comm clause declarations (e.g., x := <-ch).
					if assign, ok := clause.Comm.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
						for _, lhs := range assign.Lhs {
							if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
								if outerVars[ident.Name] {
									recordShadow(ident, fset, results, seen)
								}
								scopedVars[ident.Name] = true
							}
						}
					} else {
						collectDeclNamesStmt(clause.Comm, scopedVars)
					}
				}
				findShadowInBlock(&ast.BlockStmt{List: clause.Body}, scopedVars, fset, results, seen)
			}
		}

	case *ast.BlockStmt:
		findShadowInBlock(s, outerVars, fset, results, seen)

	case *ast.LabeledStmt:
		if s.Stmt != nil {
			findShadowInStmt(s.Stmt, outerVars, fset, results, seen)
		}

	case *ast.GoStmt, *ast.DeferStmt:
		// Check for function literals in go/defer calls.
		ast.Inspect(stmt, func(n ast.Node) bool {
			if fl, ok := n.(*ast.FuncLit); ok && fl.Body != nil {
				closureVars := copyMap(outerVars)
				if fl.Type != nil && fl.Type.Params != nil {
					collectNamesFromFieldList(fl.Type.Params, closureVars)
				}
				findShadowInBlock(fl.Body, closureVars, fset, results, seen)
				return false
			}
			return true
		})

	case *ast.ReturnStmt, *ast.ExprStmt, *ast.AssignStmt, *ast.SendStmt:
		// Check expressions for function literals (closures).
		ast.Inspect(stmt, func(n ast.Node) bool {
			if fl, ok := n.(*ast.FuncLit); ok && fl.Body != nil {
				closureVars := copyMap(outerVars)
				if fl.Type != nil && fl.Type.Params != nil {
					collectNamesFromFieldList(fl.Type.Params, closureVars)
				}
				findShadowInBlock(fl.Body, closureVars, fset, results, seen)
				return false
			}
			return true
		})
	}
}

// findShadowInBlock processes statements within a block, detecting shadowing
// and recursing into nested scopes.
func findShadowInBlock(block *ast.BlockStmt, outerVars map[string]bool,
	fset *token.FileSet, results *[]shadowInfo, seen map[string]bool) {

	localVars := copyMap(outerVars)
	for _, stmt := range block.List {
		// Check for shadowing in assignments.
		if assign, ok := stmt.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
			for _, lhs := range assign.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name == "_" {
					continue
				}
				if localVars[ident.Name] {
					recordShadow(ident, fset, results, seen)
				}
				localVars[ident.Name] = true
			}
		}
		// Check for shadowing in var/const declarations.
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if gd, ok := declStmt.Decl.(*ast.GenDecl); ok {
				for _, spec := range gd.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							if name.Name == "_" {
								continue
							}
							if localVars[name.Name] {
								recordShadow(name, fset, results, seen)
							}
							localVars[name.Name] = true
						}
					}
				}
			}
		}
		// Recurse into nested scopes.
		findShadowInStmt(stmt, localVars, fset, results, seen)
		// Collect names from this statement for siblings.
		collectTopLevelNames(stmt, localVars)
	}
}

// recordShadow adds a shadowing occurrence to results, deduplicating by name:line.
func recordShadow(ident *ast.Ident, fset *token.FileSet,
	results *[]shadowInfo, seen map[string]bool) {

	line := fset.Position(ident.Pos()).Line
	key := fmt.Sprintf("%s:%d", ident.Name, line)
	if seen[key] {
		return
	}
	seen[key] = true
	*results = append(*results, shadowInfo{
		varName: ident.Name,
		line:    line,
		isError: isErrorVarName(ident.Name),
	})
}

// collectNamesFromFieldList adds parameter/result field names to the map.
func collectNamesFromFieldList(fields *ast.FieldList, m map[string]bool) {
	if fields == nil {
		return
	}
	for _, f := range fields.List {
		for _, name := range f.Names {
			if name.Name != "" && name.Name != "_" {
				m[name.Name] = true
			}
		}
	}
}

// collectDeclNamesStmt extracts variable names from declaration init statements.
func collectDeclNamesStmt(stmt ast.Stmt, m map[string]bool) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if s.Tok == token.DEFINE {
			for _, lhs := range s.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
					m[ident.Name] = true
				}
			}
		}
	case *ast.DeclStmt:
		if gd, ok := s.Decl.(*ast.GenDecl); ok {
			for _, spec := range gd.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range vs.Names {
						if name.Name != "_" {
							m[name.Name] = true
						}
					}
				}
			}
		}
	case *ast.ExprStmt:
		// Type assertion assignments in comm clauses: x := <-ch
		// These are AssignStmts, handled above.
	}
}

// isErrorVarName returns true if the variable name is commonly an error variable.
func isErrorVarName(name string) bool {
	switch name {
	case "err", "errs", "e":
		return true
	default:
		return false
	}
}

// copyMap returns a shallow copy of a map[string]bool.
func copyMap(m map[string]bool) map[string]bool {
	result := make(map[string]bool, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
