package agent

// Value Receiver Mutation Detection in Go Code
//
// Problem: AI coding agents sometimes produce Go methods with VALUE receivers
// that modify fields on the receiver. Since value receivers operate on a COPY,
// any mutations are silently lost when the method returns. The caller never
// sees the changes, leading to subtle data-integrity bugs.
//
// Example bug:
//
//	func (c Counter) Increment() {
//	    c.count++        // mutates a COPY -- caller's Counter is unchanged
//	    c.lastUpdate = time.Now()
//	}
//
// The correct version uses a pointer receiver:
//
//	func (c *Counter) Increment() {
//	    c.count++
//	    c.lastUpdate = time.Now()
//	}
//
// Go's compiler does NOT flag this -- it compiles cleanly. The bug only
// surfaces at runtime when state unexpectedly doesn't update. This is
// especially dangerous for LLM-generated code because the model may mix
// pointer and value receiver conventions within the same type.
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (relies on external linters, inconsistent)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - staticcheck (ST1018): warns about mixed pointer/value receivers but
//     does not specifically detect mutations on value receivers
//   - go vet: does not detect this pattern
//
// Approach: AST-based analysis. For each function declaration with a VALUE
// receiver (not a pointer), walk the function body looking for:
//  1. Assignment statements (ast.AssignStmt) where the LHS selects a field
//     on the receiver name (e.g., c.field = x)
//  2. IncDec statements (ast.IncDecStmt) on a field of the receiver (c.count++)
//  3. Method calls where the receiver is passed as a pointer via & operator
//     (e.g., c.internal.Process()) -- actually this is field.method() on the
//     receiver, which may mutate nested state
//
// Only items 1 and 2 are checked to keep false positives low and complexity
// bounded. Item 3 is excluded because it requires type knowledge to know
// whether the nested method mutates.
//
// Delta-aware: only flags mutations present in the NEW content.

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

// maxValueRecvMutationWarnings limits warnings per write to avoid noise.
const maxValueRecvMutationWarnings = 4

// checkValueRecvMutation detects methods with value receivers that mutate
// receiver fields. Returns warnings for each violation.
func checkValueRecvMutation(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Collect existing mutations from oldContent for delta-aware filtering
	oldMutations := vrmCollectMutations(oldContent)

	file, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	var issues []string
	for _, decl := range file.Decls {
		if len(issues) >= maxValueRecvMutationWarnings {
			break
		}
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		vrmCheckFunc(fn, fset, filePath, &issues, oldMutations)
	}

	if len(issues) >= maxValueRecvMutationWarnings {
		issues = append(issues, fmt.Sprintf("...and potentially more value-receiver mutation(s) (showing first %d)", maxValueRecvMutationWarnings))
	}

	return issues
}

// vrmCheckFunc checks a single function declaration for value-receiver
// mutations and appends any warnings to issues. Delta-aware: skips functions
// that already had mutations in oldContent.
func vrmCheckFunc(fn *ast.FuncDecl, fset *token.FileSet, filePath string, issues *[]string, oldMutations map[string]bool) {
	if fn.Recv == nil || len(fn.Recv.List) == 0 || fn.Body == nil {
		return
	}
	recvName, isValueReceiver := vrmExtractReceiverName(fn.Recv.List[0])
	if !isValueReceiver || recvName == "" || recvName == "_" {
		return
	}

	// #3758-A: the "return the new value" idiom (time.Time.Add et al.)
	// mutates the copy ON PURPOSE and hands it back to the caller - nothing
	// is lost, and the pointer-receiver advice would actively damage a
	// correct immutable API. Exempt any function that returns the receiver
	// (or one of its fields).
	if vrmReturnsReceiver(fn.Body, recvName) {
		return
	}

	// Check if this function already had mutations in oldContent
	typeName := vrmReceiverTypeShort(fn.Recv.List[0].Type)
	sig := typeName + "." + fn.Name.Name
	if oldMutations != nil && oldMutations[sig] {
		return // Skip - this mutation already existed
	}

	for _, msg := range vrmFindReceiverMutations(fn.Body, recvName) {
		if len(*issues) >= maxValueRecvMutationWarnings {
			return
		}
		pos := fset.Position(msg.pos)
		*issues = append(*issues, fmt.Sprintf(
			"%s:%d: method (%s) %s has a VALUE receiver but mutates %s.%s -- "+
				"changes are lost (operates on a copy). Use a POINTER receiver (*%s) instead.",
			filePath, pos.Line, typeName,
			fn.Name.Name, recvName, msg.field, typeName,
		))
	}
}

// vrmCollectMutations parses source and returns a set of "TypeName.FuncName"
// signatures that already have value-receiver mutations (for delta-aware suppression).
func vrmCollectMutations(src string) map[string]bool {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	file, _, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return nil
	}
	result := make(map[string]bool)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 || fn.Body == nil {
			continue
		}
		recvName, isValueReceiver := vrmExtractReceiverName(fn.Recv.List[0])
		if !isValueReceiver || recvName == "" || recvName == "_" {
			continue
		}
		if len(vrmFindReceiverMutations(fn.Body, recvName)) > 0 {
			typeName := vrmReceiverTypeShort(fn.Recv.List[0].Type)
			result[typeName+"."+fn.Name.Name] = true
		}
	}
	return result
}

// vrmMutation records where a receiver field was mutated.
type vrmMutation struct {
	pos   token.Pos
	field string
}

// vrmExtractReceiverName returns the receiver variable name and whether the
// receiver is a value (non-pointer) type. Anonymous receivers ("_") or
// unnamed types return empty name and false.
func vrmExtractReceiverName(field *ast.Field) (name string, isValue bool) {
	if len(field.Names) == 0 {
		return "", false
	}
	name = field.Names[0].Name
	if _, isPtr := field.Type.(*ast.StarExpr); isPtr {
		return name, false
	}
	return name, true
}

// vrmReceiverTypeShort extracts a short type name from the receiver type AST
// for use in diagnostic messages. Strips package qualifiers and "*".
func vrmReceiverTypeShort(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return vrmReceiverTypeShort(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if t.Sel != nil {
			return t.Sel.Name
		}
	}
	return "?"
}

// vrmFindReceiverMutations walks the statement list (shallow, non-recursive
// into nested function literals) looking for assignments or inc/dec on
// receiver fields. Returns mutations found.
func vrmFindReceiverMutations(body *ast.BlockStmt, recvName string) []vrmMutation {
	// #3758-B: a `recvName := ...` inside the body re-binds the NAME -
	// later mutations of it hit the shadow, not the receiver. Collect the
	// rebind positions first; position-ordered comparison is conservative
	// (may skip a genuine mutation after a rebind in a sibling scope), which
	// matches this detector's keep-false-positives-low contract.
	var rebinds []token.Pos
	ast.Inspect(body, func(n ast.Node) bool {
		if _, isFuncLit := n.(*ast.FuncLit); isFuncLit {
			return false
		}
		if as, ok := n.(*ast.AssignStmt); ok && as.Tok == token.DEFINE {
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == recvName {
					rebinds = append(rebinds, as.Pos())
				}
			}
		}
		return true
	})
	isShadowed := func(p token.Pos) bool {
		for _, r := range rebinds {
			if r < p {
				return true
			}
		}
		return false
	}
	var results []vrmMutation
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		// Don't descend into nested function literals -- those have
		// their own scope and may reference the outer receiver via closure.
		if _, isFuncLit := n.(*ast.FuncLit); isFuncLit {
			return false
		}
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range stmt.Lhs {
				if field := vrmExtractRecvField(lhs, recvName); field != "" {
					if isShadowed(stmt.Pos()) {
						continue
					}
					results = append(results, vrmMutation{
						pos:   lhs.Pos(),
						field: field,
					})
				}
			}
		case *ast.IncDecStmt:
			if field := vrmExtractRecvField(stmt.X, recvName); field != "" {
				if !isShadowed(stmt.Pos()) {
					results = append(results, vrmMutation{
						pos:   stmt.X.Pos(),
						field: field,
					})
				}
			}
		}
		return true
	})
	return results
}

// vrmReturnsReceiver reports whether the function returns the receiver
// itself (or one of its fields) from any top-level return statement - the
// "return the new value" idiom that makes value-receiver mutation
// intentional (#3758-A). FuncLit returns are ignored: they return from the
// closure, not from this method.
func vrmReturnsReceiver(body *ast.BlockStmt, recvName string) bool {
	returns := false
	ast.Inspect(body, func(n ast.Node) bool {
		if _, isFuncLit := n.(*ast.FuncLit); isFuncLit {
			return false
		}
		rs, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, res := range rs.Results {
			switch e := res.(type) {
			case *ast.Ident:
				if e.Name == recvName {
					returns = true
				}
			case *ast.SelectorExpr:
				if id, ok := e.X.(*ast.Ident); ok && id.Name == recvName {
					returns = true
				}
			}
		}
		return true
	})
	return returns
}

// vrmExtractRecvField checks if an expression is a field selection on the
// receiver (e.g., "c.count"). Returns the field name if so, empty string otherwise.
func vrmExtractRecvField(expr ast.Expr, recvName string) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != recvName {
		return ""
	}
	return sel.Sel.Name
}
