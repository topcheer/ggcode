package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

const (
	paramCountThreshold = 6

	maxParamCountWarnings = 3
)

type paramCountInstance struct {
	funcName string
	pos      token.Position
	count    int
	params   []string
	paramTs  []string
	recvType string
}

func (i paramCountInstance) pcFingerprint() string {
	norm := strings.Join(i.paramTs, ",")
	return fmt.Sprintf("%s[%s]|%d|%s", i.recvType, i.funcName, i.count, norm)
}

func checkExcessiveParams(filePath, oldContent, newContent string) []string {
	if !strings.HasSuffix(filePath, ".go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	isTestFile := strings.HasSuffix(filePath, "_test.go")
	newInstances := findExcessiveParams(newContent, isTestFile)
	if len(newInstances) == 0 {
		return nil
	}

	var oldCounts map[string]int
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findExcessiveParams(oldContent, isTestFile) {
			if oldCounts == nil {
				oldCounts = make(map[string]int)
			}
			oldCounts[iss.pcFingerprint()]++
		}
	}

	var warnings []string
	newCount := 0
	for _, inst := range newInstances {

		key := inst.pcFingerprint()
		if oldCounts[key] > 0 {
			oldCounts[key]--
			continue
		}
		newCount++
		if len(warnings) < maxParamCountWarnings {
			warnings = append(warnings, fmt.Sprintf(
				"Too many parameters: function `%s` at %s has %d parameters (%s). "+
					"Functions with %d+ parameters are hard to maintain and call correctly — "+
					"consider grouping related parameters into a struct or using a builder/options pattern. "+
					"(SonarQube S107, Clean Code)",
				inst.funcName, inst.pos.String(), inst.count, strings.Join(inst.params, ", "),
				paramCountThreshold,
			))
		}
	}

	if newCount > maxParamCountWarnings {
		warnings = append(warnings, fmt.Sprintf(
			"...and %d more function(s) with %d+ parameters",
			newCount-maxParamCountWarnings, paramCountThreshold,
		))
	}

	return warnings
}

func countExcessiveParams(src string, isTestFile bool) int {
	return len(findExcessiveParams(src, isTestFile))
}

func findExcessiveParams(src string, isTestFile bool) []paramCountInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []paramCountInstance

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			inst, ok := inspectFuncDecl(node, fset, isTestFile)
			if ok {
				instances = append(instances, inst)
			}

		case *ast.FuncLit:
			if inst := inspectFuncLit(node, fset); inst != nil {
				instances = append(instances, *inst)
			}
		}
		return true
	})

	return instances
}

func inspectFuncDecl(fn *ast.FuncDecl, fset *token.FileSet, isTestFile bool) (paramCountInstance, bool) {
	if fn.Name == nil {
		return paramCountInstance{}, false
	}

	if isTestFile && isTestOrBenchFunction(fn.Name.Name) {
		return paramCountInstance{}, false
	}

	count := countParams(fn.Type.Params)
	if fn.Recv != nil {
		count += len(fn.Recv.List)
	}

	if count < paramCountThreshold {
		return paramCountInstance{}, false
	}

	var params []string
	var paramTs []string
	if fn.Recv != nil {
		params = append(params, paramNames(fn.Recv)...)
		paramTs = append(paramTs, paramTypes(fn.Recv)...)
	}
	params = append(params, paramNames(fn.Type.Params)...)
	paramTs = append(paramTs, paramTypes(fn.Type.Params)...)

	return paramCountInstance{
		funcName: fn.Name.Name,
		recvType: recvTypeText(fn.Recv),
		pos:      fset.Position(fn.Pos()),
		count:    count,
		params:   params,
		paramTs:  paramTs,
	}, true
}

func recvTypeText(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 || recv.List[0].Type == nil {
		return ""
	}
	return types.ExprString(recv.List[0].Type)
}

func inspectFuncLit(lit *ast.FuncLit, fset *token.FileSet) *paramCountInstance {
	count := countParams(lit.Type.Params)
	if count < paramCountThreshold {
		return nil
	}
	return &paramCountInstance{
		funcName: "<anonymous>",
		pos:      fset.Position(lit.Pos()),
		count:    count,
		params:   paramNames(lit.Type.Params),
		paramTs:  paramTypes(lit.Type.Params),
	}
}

func isTestOrBenchFunction(name string) bool {
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Fuzz")
}

func countParams(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, f := range fields.List {
		if len(f.Names) == 0 {

			count++
			continue
		}
		count += len(f.Names)
	}
	return count
}

func paramNames(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var names []string
	for _, f := range fields.List {
		if len(f.Names) == 0 {
			names = append(names, typeString(f.Type))
			continue
		}
		for _, name := range f.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

func paramTypes(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	n := 0
	for _, f := range fields.List {
		if len(f.Names) == 0 {
			n++
		} else {
			n += len(f.Names)
		}
	}
	ts := make([]string, 0, n)
	for _, f := range fields.List {
		if len(f.Names) == 0 {
			ts = append(ts, typeString(f.Type))
			continue
		}
		for range f.Names {
			ts = append(ts, typeString(f.Type))
		}
	}
	return ts
}

func typeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.SelectorExpr:
		if pkg, ok := t.X.(*ast.Ident); ok {
			return pkg.Name + "." + t.Sel.Name
		}
		return t.Sel.Name
	default:
		return "?"
	}
}
