package agent

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"strings"
)

var receiverAntiPatternNames = map[string]bool{
	"this": true,
	"self": true,
}

type receiverInstance struct {
	typeName string
	varName  string
	posStr   string
}

type inconsistentReceiverGroup struct {
	typeName string
	names    map[string][]receiverInstance
}

func checkReceiverConsistency(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newGroups := findReceiverInconsistencies(newContent)
	if len(newGroups) == 0 {
		return nil
	}

	oldGroups := findReceiverInconsistencies(oldContent)
	oldInconsistentTypes := make(map[string]bool)
	for _, g := range oldGroups {
		if len(g.names) > 1 {
			oldInconsistentTypes[g.typeName] = true
		}
	}

	var warnings []string
	for _, g := range newGroups {
		if len(g.names) <= 1 {
			continue
		}

		if oldInconsistentTypes[g.typeName] {
			continue
		}

		var nameList []string
		var positions []string
		for name, instances := range g.names {
			marker := name
			if receiverAntiPatternNames[name] {
				marker = fmt.Sprintf("%s (anti-pattern: use a short type-derived name instead)", name)
			}
			nameList = append(nameList, marker)
			if len(instances) > 0 {
				positions = append(positions, fmt.Sprintf("  %s: %s", name, instances[0].posStr))
			}
		}

		warnings = append(warnings, fmt.Sprintf(
			"Inconsistent receiver names on type `%s`: methods use different receiver names (%s). "+
				"Go style requires all methods on a type to use the same receiver name. "+
				"Pick one consistent short name (typically 1-2 chars derived from the type name).\n%s",
			g.typeName, strings.Join(nameList, ", "),
			strings.Join(positions, "\n")))
	}

	return warnings
}

func findReceiverInconsistencies(src string) []inconsistentReceiverGroup {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return nil
	}

	typeKey := map[string]*inconsistentReceiverGroup{}
	var orderedTypes []string

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}

		recvType := extractRecvTypeName(fn.Recv.List[0].Type)
		if recvType == "" {
			continue
		}

		varName := ""
		if len(fn.Recv.List[0].Names) > 0 {
			varName = fn.Recv.List[0].Names[0].Name
		}
		if varName == "" || varName == "_" {
			continue
		}

		pos := fset.Position(fn.Pos())

		g, exists := typeKey[recvType]
		if !exists {
			g = &inconsistentReceiverGroup{
				typeName: recvType,
				names:    make(map[string][]receiverInstance),
			}
			typeKey[recvType] = g
			orderedTypes = append(orderedTypes, recvType)
		}

		inst := receiverInstance{
			typeName: recvType,
			varName:  varName,
			posStr:   fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
		}
		g.names[varName] = append(g.names[varName], inst)
	}

	var result []inconsistentReceiverGroup
	for _, t := range orderedTypes {
		result = append(result, *typeKey[t])
	}
	return result
}

func extractRecvTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return extractRecvTypeName(e.X)
	case *ast.IndexExpr:

		return extractRecvTypeName(e.X)
	case *ast.IndexListExpr:

		return extractRecvTypeName(e.X)
	}
	return ""
}
