package agent

// Excessive Parameter Count Check (SonarQube S107 / CodeClimate "Too Many Parameters")
//
// Research basis: SonarQube rule S107 (function-parameter-count), CodeClimate
// "Too Many Parameters" engine, and Clean Code (Robert Martin) all flag functions
// with too many parameters as a maintainability risk. Functions with >5 parameters
// are harder to understand, test, and call correctly - parameter order confusion
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
	"go/types"
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
	paramTs  []string // per-param TYPE text; keys the fingerprint (#1184)
	recvType string   // receiver type text ("*Server" etc); empty for plain funcs and literals
}

// pcFingerprint keys an instance for delta suppression (fix #1142). It uses
// normalized content text (receiver TYPE + function name + parameter TYPES
// + count), NOT the line:column position - inserting a comment line above a
// function must not re-report the pre-existing function as newly introduced.
// Pattern follows pathTraversalInstance.ptFingerprint.
//
// The receiver TYPE (not the variable name) is part of the key (#1149):
// (s *Server) handle and (s *Client) handle are different functions and must
// not collide just because both receivers are named `s`.
//
// Parameter NAMES are deliberately excluded from the key (#1184): the smell
// (too many params, SonarQube S107) is identical before and after a pure
// parameter rename, so name-based keys made every renamed param miss its
// old entry and re-reported the pre-existing instance as newly introduced -
// the same delta-contract violation family as #1179. Types + count carry
// the whole semantic identity of the smell; display still uses names.
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

	// Delta-aware: only flag newly introduced instances (fix #142).
	// #1187: the Test*/Benchmark* name exemption applies only to _test.go
	// files - go test only ever compiles such functions from _test.go, so a
	// Test-prefixed function in production code (TestMatch, TestIMConnection,
	// ...) must still be checked.
	isTestFile := strings.HasSuffix(filePath, "_test.go")
	newInstances := findExcessiveParams(newContent, isTestFile)
	if len(newInstances) == 0 {
		return nil
	}

	// Delta suppression is multiset/count-based, not set-based (#1149): a
	// plain set merges N colliding old instances (same fingerprint - e.g. two
	// anonymous literals with identical params) into one entry and silently
	// absorbs a NEW (N+1)-th instance. Each old occurrence consumes one match;
	// only the surplus over the old count is reported as new.
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
		// Delta-suppress pre-existing instances by content fingerprint so a
		// pure line shift (comment insertion above) stays silent (#1142).
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

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
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
		// #1171: inspect every function literal wherever it appears -
		// var declarations, call arguments, go/defer statements - not only
		// direct RHS elements of `:=` assignments. ast.Inspect descends
		// into nested literals individually, so removing the old
		// AssignStmt branch avoids double counting.
		case *ast.FuncLit:
			if inst := inspectFuncLit(node, fset); inst != nil {
				instances = append(instances, *inst)
			}
		}
		return true
	})

	return instances
}

// inspectFuncDecl checks a named function declaration for excessive params.
// Returns the instance and true if it should be flagged.
func inspectFuncDecl(fn *ast.FuncDecl, fset *token.FileSet, isTestFile bool) (paramCountInstance, bool) {
	if fn.Name == nil {
		return paramCountInstance{}, false
	}
	// #1187: exempt Test/Benchmark names only in _test.go files; production
	// code with such names (health probes, connection testers) stays checked.
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
		recvType: recvTypeText(fn.Recv), // #1149: type, not the variable name
		pos:      fset.Position(fn.Pos()),
		count:    count,
		params:   params,
		paramTs:  paramTs,
	}, true
}

// recvTypeText renders the receiver type ("*Server", "Client") for the
// delta fingerprint (#1149). Empty for nil receivers.
func recvTypeText(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 || recv.List[0].Type == nil {
		return ""
	}
	return types.ExprString(recv.List[0].Type)
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
		paramTs:  paramTypes(lit.Type.Params),
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

// paramTypes returns one TYPE text per parameter (#1184): each name
// occurrence in a grouped field ("a, b int") contributes the field's type,
// and unnamed parameters contribute their type directly. This is the
// rename-stable identity used by pcFingerprint, unlike paramNames (display
// only). Prealloc: len is an upper bound of entries per field list.
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
