package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

const maxValueRecvMutationWarnings = 4

func checkValueRecvMutation(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

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

func vrmCheckFunc(fn *ast.FuncDecl, fset *token.FileSet, filePath string, issues *[]string, oldMutations map[string]bool) {
	if fn.Recv == nil || len(fn.Recv.List) == 0 || fn.Body == nil {
		return
	}
	recvName, isValueReceiver := vrmExtractReceiverName(fn.Recv.List[0])
	if !isValueReceiver || recvName == "" || recvName == "_" {
		return
	}

	typeName := vrmReceiverTypeShort(fn.Recv.List[0].Type)

	if vrmReturnsReceiver(fn, typeName) {
		return
	}

	sig := typeName + "." + fn.Name.Name
	if oldMutations != nil && oldMutations[sig] {
		return
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

type vrmMutation struct {
	pos   token.Pos
	field string
}

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

func vrmFindReceiverMutations(body *ast.BlockStmt, recvName string) []vrmMutation {
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

func vrmReturnsReceiver(fn *ast.FuncDecl, typeName string) bool {
	if fn.Type == nil || fn.Type.Results == nil {
		return false
	}
	for _, r := range fn.Type.Results.List {
		if id, ok := r.Type.(*ast.Ident); ok && id.Name == typeName {
			return true
		}
	}
	return false
}

func vrmExtractRecvField(expr ast.Expr, recvName string) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}

	if _, isSel := sel.X.(*ast.SelectorExpr); isSel {
		return ""
	}
	base := sel.X
	ident, ok := base.(*ast.Ident)
	if !ok || ident.Name != recvName {
		return ""
	}
	return sel.Sel.Name
}
