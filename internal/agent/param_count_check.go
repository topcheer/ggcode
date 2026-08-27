package agent

// Excessive Parameter Count Check (SonarQube S107 / CodeClimate "Too Many Parameters")
//
// Research basis: SonarQube rule S107 (function-parameter-count), CodeClimate
// "Too Many Parameters" engine, and Clean Code (Robert Martin) all flag functions
// with too many parameters as a maintainability risk. Functions with >5 parameters
// are harder to understand, test, and call correctly — parameter order confusion
// and missing arguments are common bug sources.
//
// This check fills a gap in ggcode's quality intelligence: while we have
// complexity_gate (cyclomatic complexity) and various pattern checks, we do NOT
// detect parameter-count code smells at write time.
//
// Design:
//   - Zero-LLM-cost: deterministic AST analysis (go/ast)
//   - Delta-aware: only flags NEW instances introduced by this edit
//   - Threshold: 6+ parameters (including receiver for methods) - matches
//     SonarQube's default, allows common Go idioms (5 params OK)
//   - Capped at 3 warnings per file to avoid context flooding

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

const (
	// paramCountThreshold is the maximum number of parameters before flagging.
	// SonarQube default is 7 (including receiver), but we use 6 as Go functions
	// tend to have one fewer formal parameter (receiver is separate).
	paramCountThreshold = 6

	// maxParamCountWarnings caps warnings per file.
	maxParamCountWarnings = 3
)

type paramCountInstance struct {
	funcName string
	pos      token.Position
	count    int
	params   []string
}

// pcFingerprint keys an instance for delta suppression (fix #1142). It uses
// normalized content text (function name + parameter names/types), NOT the
// line:column position - inserting a comment line above a function must not
// re-report the pre-existing function as newly introduced. Pattern follows
// pathTraversalInstance.ptFingerprint.
func (i paramCountInstance) pcFingerprint() string {
	norm := strings.Join(i.params, ",")
	return fmt.Sprintf("%s|%d|%s", i.funcName, i.count, norm)
}

func checkExcessiveParams(filePath, oldContent, newContent string) []string {
	if !strings.HasSuffix(filePath, ".go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Delta-aware: only flag newly introduced instances (fix #142).
	newInstances := findExcessiveParams(newContent)
	if len(newInstances) == 0 {
		return nil
	}

	var oldKeys map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findExcessiveParams(oldContent) {
			if oldKeys == nil {
				oldKeys = make(map[string]bool)
			}
			oldKeys[iss.pcFingerprint()] = true
		}
	}

	var warnings []string
	newCount := 0
	for _, inst := range newInstances {
		// Delta-suppress pre-existing instances by content fingerprint so a
		// pure line shift (comment insertion above) stays silent (#1142).
		key := inst.pcFingerprint()
		if oldKeys != nil && oldKeys[key] {
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

func countExcessiveParams(src string) int {
	return len(findExcessiveParams(src))
}

func findExcessiveParams(src string) []paramCountInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []paramCountInstance

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			inst, ok := inspectFuncDecl(node, fset)
			if ok {
				instances = append(instances, inst)
			}
		case *ast.AssignStmt:
			for _, e := range node.Rhs {
				if lit, ok := e.(*ast.FuncLit); ok {
					if inst := inspectFuncLit(lit, fset); inst != nil {
						instances = append(instances, *inst)
					}
				}
			}
		}
		return true
	})

	return instances
}

// inspectFuncDecl checks a named function declaration for excessive params.
// Returns the instance and true if it should be flagged.
func inspectFuncDecl(fn *ast.FuncDecl, fset *token.FileSet) (paramCountInstance, bool) {
	if fn.Name == nil || isTestOrBenchFunction(fn.Name.Name) {
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
	if fn.Recv != nil {
		params = append(params, paramNames(fn.Recv)...)
	}
	params = append(params, paramNames(fn.Type.Params)...)

	return paramCountInstance{
		funcName: fn.Name.Name,
		pos:      fset.Position(fn.Pos()),
		count:    count,
		params:   params,
	}, true
}

// inspectFuncLit checks an anonymous function literal for excessive params.
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
	}
}

// isTestOrBenchFunction returns true for Test/Benchmark function names.
func isTestOrBenchFunction(name string) bool {
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark")
}

// countParams counts the total number of named parameters in a FieldList.
// In Go AST, a field like `a, b, c int` counts as 3 params.
func countParams(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, f := range fields.List {
		if len(f.Names) == 0 {
			// Unnamed parameter (e.g., in function types)
			count++
			continue
		}
		count += len(f.Names)
	}
	return count
}

// paramNames returns parameter names from a FieldList.
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

// typeString produces a short type name for display.
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
