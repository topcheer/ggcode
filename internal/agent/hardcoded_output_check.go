package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"strings"
)

const hardcodedOutputMaxWarnings = 2

const (
	minHardcodedEntries = 5
	maxNonLiteralRatio  = 0.3
)

func checkHardcodedOutput(fp, oldContent, newContent string) []string {
	if isTestFile(fp) {
		return nil
	}

	lang := detectLanguageFromPath(fp)
	var warnings []string

	switch lang {
	case LangGo:
		warnings = checkHardcodedOutputGo(fp, newContent)
	case LangPython:
		warnings = checkHardcodedOutputPython(newContent)
	case LangJSTS:
		warnings = checkHardcodedOutputJSTS(newContent)
	}

	if strings.TrimSpace(oldContent) != "" && len(warnings) > 0 {
		var oldWarnings []string
		switch lang {
		case LangGo:
			oldWarnings = checkHardcodedOutputGo(fp, oldContent)
		case LangPython:
			oldWarnings = checkHardcodedOutputPython(oldContent)
		case LangJSTS:
			oldWarnings = checkHardcodedOutputJSTS(oldContent)
		}
		oldSet := make(map[string]bool, len(oldWarnings))
		for _, w := range oldWarnings {
			oldSet[w] = true
		}
		var fresh []string
		for _, w := range warnings {
			if !oldSet[w] {
				fresh = append(fresh, w)
			}
		}
		warnings = fresh
	}

	if len(warnings) > hardcodedOutputMaxWarnings {
		warnings = warnings[:hardcodedOutputMaxWarnings]
	}
	return warnings
}

func checkHardcodedOutputGo(fp, src string) []string {
	file, _, err := parseGoSource(fp, src, 0)
	if err != nil || file == nil {

		return checkHardcodedOutputString(src)
	}

	var warnings []string

	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, val := range vs.Values {
				cl, ok := val.(*ast.CompositeLit)
				if !ok || len(cl.Elts) < minHardcodedEntries {
					continue
				}
				if allLiteralKV(cl.Elts) {
					name := "outputs"
					if len(vs.Names) > 0 {
						name = vs.Names[0].Name
					}
					if isLikelyConfigGetter(name) {
						continue
					}
					warnings = append(warnings, formatHardcodedWarning(
						name, "package-level hardcoded map literal", len(cl.Elts)))
				}
			}
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}

		info := analyzeGoFuncBody(fn.Body)
		if info.suspicious {

			name := fn.Name.Name
			if isLikelyConfigGetter(name) {
				return true
			}
			warnings = append(warnings, formatHardcodedWarning(name, info.reason, info.entryCount))
		}
		return true
	})

	return warnings
}

type hardcodedFuncInfo struct {
	suspicious bool
	reason     string
	entryCount int
}

func analyzeGoFuncBody(body *ast.BlockStmt) hardcodedFuncInfo {
	var literalEntries int
	var totalStmts int
	var hasMapLiteral bool
	var hasSwitchLiterals bool

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:

			if node.Type != nil && len(node.Elts) >= minHardcodedEntries {
				if allLiteralKV(node.Elts) {
					literalEntries += len(node.Elts)
					hasMapLiteral = true
				}
			}
		case *ast.CaseClause:

			if len(node.List) > 0 && node.Body != nil {
				if allReturnLiterals(node.Body) {
					literalEntries++
					hasSwitchLiterals = true
				}
			}
		case *ast.ExprStmt:
			totalStmts++
		case *ast.AssignStmt:
			totalStmts++
		case *ast.ReturnStmt:
			totalStmts++
		case *ast.IfStmt:
			totalStmts++
		}
		return true
	})

	if literalEntries >= minHardcodedEntries {

		if totalStmts > 0 {
			ratio := float64(totalStmts-literalEntries) / float64(totalStmts)
			if ratio > maxNonLiteralRatio {
				return hardcodedFuncInfo{suspicious: false}
			}
		}

		reason := "large hardcoded mapping"
		if hasMapLiteral {
			reason = "map literal of hardcoded input-to-output entries"
		} else if hasSwitchLiterals {
			reason = "switch/if-else chain returning hardcoded literals"
		}

		return hardcodedFuncInfo{
			suspicious: true,
			reason:     reason,
			entryCount: literalEntries,
		}
	}

	return hardcodedFuncInfo{suspicious: false}
}

func allLiteralKV(elts []ast.Expr) bool {
	count := 0
	for _, elt := range elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return false
		}
		if !isBasicLiteral(kv.Key) || !isBasicLiteral(kv.Value) {
			return false
		}
		count++
	}
	return count >= minHardcodedEntries
}

func allReturnLiterals(body []ast.Stmt) bool {
	if len(body) == 0 {
		return false
	}
	for _, stmt := range body {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			return false
		}
		for _, result := range ret.Results {
			if !isBasicLiteral(result) {
				return false
			}
		}
	}
	return true
}

func isBasicLiteral(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:

		name := expr.(*ast.Ident).Name
		return name == "true" || name == "false" || name == "nil"
	default:
		return false
	}
}

func isLikelyConfigGetter(name string) bool {
	lower := strings.ToLower(name)
	configPrefixes := []string{
		"default", "config", "registry", "init", "new", "create",
		"setup", "register", "known", "supported", "available",
		"builtin", "predefined", "constant",
	}
	for _, p := range configPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}

	referenceSuffixes := []string{
		"names", "symbols", "text", "texts", "labels", "codes",
		"titles", "months", "days", "units", "statuses",
	}
	for _, s := range referenceSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}

func checkHardcodedOutputPython(src string) []string {
	var warnings []string

	dictRe := regexp.MustCompile(`(?s)\{([^{}]*?)\}`)
	for _, m := range dictRe.FindAllStringSubmatch(src, -1) {
		if len(m) < 2 {
			continue
		}

		pairRe := regexp.MustCompile(`("[^"]*"|'[^']*')\s*:\s*("[^"]*"|'[^']*'|[^,}]+)`)
		entries := len(pairRe.FindAllString(m[1], -1))
		if entries >= minHardcodedEntries {
			if allStringLiterals(m[1]) {
				warnings = append(warnings, fmt.Sprintf(
					"large dict literal with %d hardcoded string-to-literal entries - "+
						"verify this implements real logic, not input memorization",
					entries))
				break
			}
		}
	}

	ifElseChainRe := regexp.MustCompile(`(?m)^\s+if\s+\w+\s*==\s*["']`)
	chains := ifElseChainRe.FindAllString(src, -1)
	if len(chains) >= minHardcodedEntries {
		warnings = append(warnings, fmt.Sprintf(
			"if/elif chain with %d hardcoded string comparisons - "+
				"verify this implements real logic, not input memorization",
			len(chains)))
	}

	return warnings
}

func checkHardcodedOutputJSTS(src string) []string {
	var warnings []string

	objRe := regexp.MustCompile(`(?m)\{([^{}]{200,})\}`)
	for _, m := range objRe.FindAllStringSubmatch(src, -1) {
		if len(m) < 2 {
			continue
		}

		kvRe := regexp.MustCompile(`["'` + "`" + `][^"'` + "`" + `]+["'` + "`" + `]\s*:\s*["'` + "`" + `]`)
		count := len(kvRe.FindAllString(m[1], -1))
		if count >= minHardcodedEntries {
			warnings = append(warnings, fmt.Sprintf(
				"large object literal with %d hardcoded string-literal entries - "+
					"verify this implements real logic, not test input memorization",
				count))
			break
		}
	}

	switchRe := regexp.MustCompile(`(?m)case\s+["'` + "`" + `]`)
	cases := len(switchRe.FindAllString(src, -1))
	if cases >= minHardcodedEntries {

		returnLiteralRe := regexp.MustCompile(`return\s+["'` + "`" + `]`)
		literalReturns := len(returnLiteralRe.FindAllString(src, -1))
		if literalReturns >= minHardcodedEntries {
			warnings = append(warnings, fmt.Sprintf(
				"switch statement with %d hardcoded string cases returning literals - "+
					"verify this implements real logic, not input memorization",
				cases))
		}
	}

	return warnings
}

func checkHardcodedOutputString(src string) []string {

	mapRe := regexp.MustCompile(`map\[string\]string\{([^}]+)\}`)
	var warnings []string
	for _, m := range mapRe.FindAllStringSubmatch(src, -1) {
		if len(m) < 2 {
			continue
		}
		kvCount := strings.Count(m[1], `": "`) + strings.Count(m[1], `":"`)
		if kvCount >= minHardcodedEntries {
			warnings = append(warnings, fmt.Sprintf(
				"large map[string]string with %d hardcoded entries - "+
					"verify this implements real logic, not input memorization",
				kvCount))
			break
		}
	}
	return warnings
}

func allStringLiterals(s string) bool {

	quoted := strings.Count(s, `"`) + strings.Count(s, `'`)
	total := len(strings.Fields(s))
	if total == 0 {
		return false
	}
	ratio := float64(quoted) / float64(total*2)
	return ratio > 0.3
}

func detectLanguageFromPath(fp string) Language {
	lower := strings.ToLower(fp)
	switch {
	case strings.HasSuffix(lower, ".go"):
		return LangGo
	case strings.HasSuffix(lower, ".py"):
		return LangPython
	case strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".ts"),
		strings.HasSuffix(lower, ".jsx"), strings.HasSuffix(lower, ".tsx"):
		return LangJSTS
	default:
		return 0
	}
}

func formatHardcodedWarning(funcName, reason string, entryCount int) string {
	return fmt.Sprintf(
		"function %s contains %s (%d entries) - "+
			"this may be 'lookup-table memorization' (hardcoding test inputs-to-outputs "+
			"instead of implementing general logic). Research (SpecBench arXiv:2605.21384) "+
			"shows this is the most severe form of reward hacking in coding agents: "+
			"passes visible tests but fails on any novel input. Verify the function "+
			"implements real computation.",
		funcName, reason, entryCount)
}
