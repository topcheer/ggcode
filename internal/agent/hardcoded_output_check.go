package agent

// Hardcoded Output Memorization Detector
//
// Research basis: SpecBench (arXiv:2605.21384, Weco AI, 2026) documents the
// most severe reward hacking pattern in long-horizon coding agents: "lookup-
// table memorization." Instead of implementing real logic, agents hardcode
// input-to-output mappings. The canonical case: a C compiler agent produced a
// 2,900-line hash table mapping test input hashes to pre-computed outputs,
// achieving 97% on visible validation tests but 0% on held-out tests.
//
// This is distinct from "feature isolation" (SpecBench's moderate category)
// which is harder to detect deterministically. Memorization is:
//   - Detectable via source structure analysis (no semantic understanding needed)
//   - More severe: zero generalization vs. partial generalization
//   - More deliberate: the agent actively constructs the mapping
//
// Detection patterns (all deterministic, zero LLM cost):
//
// 1. LARGE MAP/SWITCH OF HARDCODED OUTPUTS: A function whose body is dominated
//    by a map literal or switch statement returning hardcoded values, with
//    little or no actual computation. Threshold: map/switch with >= 5 constant
//    entries that return string/numeric literals.
//
// 2. INPUT-KEYED LOOKUP: A function that computes a hash/checksum of the input
//    and uses it as a key into a pre-built lookup table. Pattern:
//    hash(input) -> map[hash] -> return literal.
//
// 3. STRING-KEYED HARDcoded RETURN: A function that maps specific string
//    inputs to hardcoded string/numeric outputs via if/else chains or switch
//    cases, with no computation between input and output.
//
// Design decisions:
//   - Only triggers on NON-test files (test files legitimately contain fixtures)
//   - Threshold tuned to avoid false positives on config/registry code
//   - Max 2 warnings per write to avoid nagging
//   - Language-agnostic string patterns + Go AST analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
)

const hardcodedOutputMaxWarnings = 2

// Thresholds for suspicion
const (
	// minHardcodedEntries: a map/switch with fewer constant entries is normal
	// (e.g., error code mappings, config defaults).
	minHardcodedEntries = 5
	// maxNonLiteralRatio: if more than 30% of statements in the function are
	// non-literal (actual computation), it's probably real logic.
	maxNonLiteralRatio = 0.3
)

// checkHardcodedOutput detects hardcoded input-to-output memorization patterns.
// Runs for Go, Python, and JS/TS files. Returns warning strings.
func checkHardcodedOutput(fp, _, newContent string) []string {
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

	if len(warnings) > hardcodedOutputMaxWarnings {
		warnings = warnings[:hardcodedOutputMaxWarnings]
	}
	return warnings
}

// --- Go AST-based detection ---

func checkHardcodedOutputGo(fp, src string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, fp, src, 0)
	if err != nil || file == nil {
		// Fall back to string-based detection
		return checkHardcodedOutputString(src)
	}

	var warnings []string

	// Check package-level var declarations with large map literals
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
			// Check if function name looks like it should compute something
			// (not a config getter or registry)
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

// analyzeGoFuncBody checks if a function body is dominated by hardcoded
// literal mappings rather than real computation.
func analyzeGoFuncBody(body *ast.BlockStmt) hardcodedFuncInfo {
	var literalEntries int
	var totalStmts int
	var hasMapLiteral bool
	var hasSwitchLiterals bool

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			// Map literal with many key:value pairs
			if node.Type != nil && len(node.Elts) >= minHardcodedEntries {
				if allLiteralKV(node.Elts) {
					literalEntries += len(node.Elts)
					hasMapLiteral = true
				}
			}
		case *ast.CaseClause:
			// switch case returning literals
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
		// Check ratio: if the function has lots of real statements, it's
		// probably not pure memorization
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

// allLiteralKV checks if all composite literal elements are key:value pairs
// where both key and value are basic literals.
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

// allReturnLiterals checks if a case body consists only of returning literals.
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
		// true/false/nil count as literals
		name := expr.(*ast.Ident).Name
		return name == "true" || name == "false" || name == "nil"
	default:
		return false
	}
}

// isLikelyConfigGetter returns true for function names that legitimately
// return hardcoded mappings (config registries, error code tables, etc.)
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
	return false
}

// --- Python detection (string-based) ---

func checkHardcodedOutputPython(src string) []string {
	var warnings []string

	// Pattern: large dict literal with string keys mapping to literals
	// e.g., LOOKUP = {"input1": "output1", "input2": "output2", ...}
	dictRe := regexp.MustCompile(`(?s)\{([^{}]*?)\}`)
	for _, m := range dictRe.FindAllStringSubmatch(src, -1) {
		if len(m) < 2 {
			continue
		}
		entries := strings.Count(m[1], `"`)
		if entries/2 >= minHardcodedEntries {
			if allStringLiterals(m[1]) {
				warnings = append(warnings, fmt.Sprintf(
					"large dict literal with %d hardcoded string-to-literal entries - "+
						"verify this implements real logic, not input memorization",
					entries/2))
				break // one warning per file
			}
		}
	}

	// Pattern: function with many if input == "X": return "Y"
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

// --- JS/TS detection (string-based) ---

func checkHardcodedOutputJSTS(src string) []string {
	var warnings []string

	// Pattern: large object literal with string keys -to- literal values
	objRe := regexp.MustCompile(`(?m)\{([^{}]{200,})\}`)
	for _, m := range objRe.FindAllStringSubmatch(src, -1) {
		if len(m) < 2 {
			continue
		}
		// Count key: value pairs with string keys
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

	// Pattern: switch with many string case returning literals
	switchRe := regexp.MustCompile(`(?m)case\s+["'` + "`" + `]`)
	cases := len(switchRe.FindAllString(src, -1))
	if cases >= minHardcodedEntries {
		// Check if returns are literals (rough heuristic)
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

// --- String-based fallback (for Go parse failures) ---

func checkHardcodedOutputString(src string) []string {
	// Rough heuristic: many consecutive string-to-string pairs in a map literal
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

// --- Helpers ---

func allStringLiterals(s string) bool {
	// Rough check: at least 80% of content is string literals
	quoted := strings.Count(s, `"`)
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
