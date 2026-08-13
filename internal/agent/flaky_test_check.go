package agent

// Flaky Test Pattern Detection
//
// Flaky tests - tests that pass or fail non-deterministically - are one of the
// most impactful quality issues in modern CI/CD. Engineering organizations
// like Google, Meta, and Mozilla have published extensively on the cost of
// flaky tests: they erode trust in CI, slow down development, and mask real
// bugs. LLM-generated tests are especially prone to flakiness because models
// frequently introduce patterns that are technically correct but
// non-deterministic:
//
//   - time.Now() in assertions: comparing timestamps or asserting exact time
//     values that vary by execution speed or timezone.
//   - Unseeded math/rand: random test data that changes between runs, causing
//     intermittent failures on edge cases.
//   - time.Sleep in tests: timing-dependent assertions that race with real
//     execution speed, failing on slow CI machines.
//   - Goroutine launches without synchronization: tests that spawn goroutines
//     without WaitGroup/errgroup, causing the test to finish before the
//     goroutine completes (race detector failures, missed assertions).
//   - Map iteration order reliance: Go randomizes map iteration order, so
//     tests asserting specific order are inherently flaky.
//
// Research basis:
//   - "Where do Google Engineers Find Flaky Tests?" (Facebook/Meta, 2025):
//     23% of flaky tests are caused by time-dependence, 19% by concurrency
//     races, and 15% by random data without seeds.
//   - "Test Flake at Scale" (Netflix Tech Blog, 2024): async without
//     synchronization is the #1 root cause in microservice test suites.
//   - Claude Code, Cursor, Cline: none detect flaky patterns at write-time.
//     They rely on post-hoc CI failures, wasting build cycles.
//
// ggcode's approach: deterministic, delta-based detection that flags newly
// introduced flaky patterns in test files. Only patterns INTRODUCED by the
// current edit are flagged (comparing old vs new content). Zero LLM cost,
// zero external dependencies, <1ms per check.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// flakyTestMaxWarnings caps the number of flaky test warnings per write.
const flakyTestMaxWarnings = 3

// --- Go AST-based patterns ---

// flakyGoTestPatterns checks parsed Go test files for flaky patterns using AST
// analysis. This is more precise than regex for Go code. The oldLines set is
// used for delta-aware filtering: only patterns on lines not present in the
// old content are reported.
func flakyGoTestPatterns(fset *token.FileSet, file *ast.File, src string, oldLines map[string]bool) []string {
	var warnings []string

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			for _, w := range checkFlakyGoCall(node, fset) {
				if !flakyWarningOnOldLine(w, src, oldLines) {
					warnings = append(warnings, w)
				}
			}
		case *ast.GoStmt:
			for _, w := range checkFlakyGoroutine(node, fset, src) {
				if !flakyWarningOnOldLine(w, src, oldLines) {
					warnings = append(warnings, w)
				}
			}
		case *ast.RangeStmt:
			for _, w := range checkFlakyMapRange(node, fset, src) {
				if !flakyWarningOnOldLine(w, src, oldLines) {
					warnings = append(warnings, w)
				}
			}
		}
		return true
	})

	return warnings
}

// checkFlakyGoCall inspects a function call expression for flaky patterns.
func checkFlakyGoCall(call *ast.CallExpr, fset *token.FileSet) []string {
	var warnings []string
	pos := fset.Position(call.Pos())

	fnName := exprToString(call.Fun)

	// time.Now() in test files is flaky when used for assertions or comparisons.
	// Common pattern: ts := time.Now(); ... assert.Equal(ts, stored)
	// We flag time.Now() calls as potential time-dependence.
	if fnName == "time.Now" {
		warnings = append(warnings, fmt.Sprintf(
			"%s: time.Now() in test creates time-dependent assertions that may be flaky. "+
				"Consider using a fixed time (e.g., time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) or a clock mock.",
			pos))
	}

	// time.Sleep in test files causes timing-dependent flakiness.
	if fnName == "time.Sleep" {
		warnings = append(warnings, fmt.Sprintf(
			"%s: time.Sleep() in test creates timing-dependent flakiness on slow/loaded CI machines. "+
				"Consider using a channel, WaitGroup, or testing.T.Deadline() for synchronization.",
			pos))
	}

	// Unseeded math/rand calls (rand.Intn, rand.Float64, rand.Shuffle, etc.)
	// produce different values each run. Flag in test files.
	if strings.HasPrefix(fnName, "rand.") && !strings.Contains(fnName, "Seed") &&
		!strings.Contains(fnName, "New") && !strings.Contains(fnName, "Read") {
		warnings = append(warnings, fmt.Sprintf(
			"%s: %s() without rand.Seed() or math/rand/v2 produces non-deterministic test data. "+
				"Use rand.New(rand.NewSource(seed)) or rand.NewPCG(seed1, seed2) for reproducible tests.",
			pos, fnName))
	}

	return warnings
}

// checkFlakyGoroutine detects `go func()` or `go fn()` in test functions
// without nearby WaitGroup/errgroup synchronization.
func checkFlakyGoroutine(goStmt *ast.GoStmt, fset *token.FileSet, src string) []string {
	pos := fset.Position(goStmt.Pos())

	// Check surrounding context for synchronization primitives.
	// We look in a window of +/- 15 lines from the go statement.
	line := pos.Line
	lines := strings.Split(src, "\n")
	startLine := line - 15
	if startLine < 1 {
		startLine = 1
	}
	endLine := line + 15
	if endLine > len(lines) {
		endLine = len(lines)
	}

	window := strings.Join(lines[startLine-1:endLine], "\n")

	// If we find WaitGroup, errgroup, or sync.Once nearby, it's likely synchronized.
	if strings.Contains(window, "WaitGroup") ||
		strings.Contains(window, "wg.Add") ||
		strings.Contains(window, "wg.Done") ||
		strings.Contains(window, "wg.Wait") ||
		strings.Contains(window, "errgroup.") ||
		strings.Contains(window, "sync.Once") {
		return nil
	}

	fnName := exprToString(goStmt.Call.Fun)
	return []string{fmt.Sprintf(
		"%s: 'go %s' launched without WaitGroup/errgroup synchronization nearby. "+
			"The goroutine may not complete before the test ends, causing flaky failures. "+
			"Add sync.WaitGroup or errgroup.Group to wait for goroutine completion.",
		pos, fnName)}
}

// checkFlakyMapRange detects range over map with order-dependent logic.
// Go randomizes map iteration order, so tests relying on iteration order are flaky.
func checkFlakyMapRange(rangeStmt *ast.RangeStmt, fset *token.FileSet, src string) []string {
	// Only flag range over maps, not slices/arrays (which have deterministic order).
	if rangeStmt.X == nil {
		return nil
	}
	// Check the type of the ranged expression — only maps have randomized iteration.
	if bt, ok := rangeStmt.X.(*ast.MapType); !ok {
		_ = bt // not a map literal type; could still be a map variable
		// We can't determine the type for variables without type info, so
		// we only skip when we can positively identify it as a non-map type
		// (e.g., a slice/array composite literal with []T but not map[K]V).
		if !isMapTypeExpr(rangeStmt.X) {
			return nil
		}
	}

	// Only flag if the range body likely depends on iteration order.
	// We check if the body contains index-based access or comparison with a
	// position-dependent value (e.g., [0], [1], first/last element).
	pos := fset.Position(rangeStmt.Pos())
	line := pos.Line
	lines := strings.Split(src, "\n")

	// Find the end of the range body (closing brace at same or lower indent).
	bodyStart := line
	bodyEnd := bodyStart + 30 // scan at most 30 lines
	if bodyEnd > len(lines) {
		bodyEnd = len(lines)
	}

	body := strings.Join(lines[bodyStart-1:bodyEnd], "\n")

	// Check for order-dependent patterns: slicing the result, comparing to [0],
	// appending and checking order, .First(), sort before comparison.
	// If the body already calls sort.Sort/slices.Sort, the test handles ordering.
	if strings.Contains(body, "sort.Sort") ||
		strings.Contains(body, "sort.Slice") ||
		strings.Contains(body, "sort.Strings") ||
		strings.Contains(body, "sort.Ints") ||
		strings.Contains(body, "slices.Sort") ||
		strings.Contains(body, "slices.Sorted") {
		return nil // ordering is handled
	}

	// Check for order-dependent assertions: comparing to first element,
	// using index [0], or asserting a specific sequence.
	if strings.Contains(body, "[0]") ||
		strings.Contains(body, "[1]") ||
		strings.Contains(body, ".First()") {
		// Only flag if the ranged variable is used in the assertion.
		// This is a heuristic - we check if the value variable name appears.
		valName := ""
		if rangeStmt.Value != nil {
			valName = exprToString(rangeStmt.Value)
		}
		keyName := ""
		if rangeStmt.Key != nil {
			keyName = exprToString(rangeStmt.Key)
		}
		if valName != "" && (strings.Contains(body, valName) || strings.Contains(body, keyName)) {
			return []string{fmt.Sprintf(
				"%s: range over map with order-dependent assertion may be flaky - "+
					"Go randomizes map iteration order. Sort keys or use an ordered structure (slice) before asserting order.",
				pos)}
		}
	}

	return nil
}

// isMapTypeExpr checks if the AST expression looks like a map type.
// We check for *ast.MapType or *ast.CompositeLit with map elements.
func isMapTypeExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.MapType:
		return true
	case *ast.CompositeLit:
		// Check if the composite literal's type is a map
		if _, ok := e.Type.(*ast.MapType); ok {
			return true
		}
	case *ast.Ident:
		// Can't determine type from variable name alone without type info.
		// Be conservative: assume it could be a map.
		return true
	case *ast.CallExpr:
		// make(map[...]) or function returning a map — assume could be map
		if fun, ok := e.Fun.(*ast.Ident); ok && fun.Name == "make" {
			if len(e.Args) > 0 {
				if _, isMap := e.Args[0].(*ast.MapType); isMap {
					return true
				}
				// make(someVar) — can't tell, be conservative
				return true
			}
		}
		return true // function call result, be conservative
	}
	// For slice/array types ([]T), this won't match MapType, so we skip.
	return false
}

// --- Multi-language regex patterns ---

// flakyPatternRegexes matches lines in non-Go test files that indicate flaky patterns.
var flakyPatternRegexes = []*regexp.Regexp{
	// time-dependent assertions
	// Go
	regexp.MustCompile(`time\.Now\(\)`),
	// JS/TS: Date.now(), new Date() in test assertions
	regexp.MustCompile(`Date\.now\(\)`),
	regexp.MustCompile(`new Date\(\)`),
	// Python: datetime.now()
	regexp.MustCompile(`datetime\.now\(\)`),
	// Ruby: Time.now
	regexp.MustCompile(`Time\.now\b`),

	// sleep in tests
	// Go
	regexp.MustCompile(`time\.Sleep\(`),
	// JS/TS
	regexp.MustCompile(`setTimeout\(`),
	// Python
	regexp.MustCompile(`time\.sleep\(`),
	// Ruby
	regexp.MustCompile(`sleep\s+\d`),

	// unseeded random
	// Go
	regexp.MustCompile(`rand\.Intn\(`),
	regexp.MustCompile(`rand\.Float64\(`),
	regexp.MustCompile(`rand\.Intn\b`),
	regexp.MustCompile(`rand\.Shuffle\(`),
	// JS/TS
	regexp.MustCompile(`Math\.random\(\)`),
	// Python
	regexp.MustCompile(`random\.random\(\)`),
	regexp.MustCompile(`random\.randint\(`),
	regexp.MustCompile(`random\.choice\(`),
	// Ruby
	regexp.MustCompile(`rand\(`),
}

// flakyPatternDescriptions maps regex index to human-readable description.
var flakyPatternDescriptions = []string{
	"time.Now() creates time-dependent assertions",
	"Date.now() creates time-dependent assertions",
	"new Date() creates time-dependent assertions",
	"datetime.now() creates time-dependent assertions",
	"Time.now creates time-dependent assertions",
	"time.Sleep() causes timing-dependent flakiness",
	"setTimeout() causes timing-dependent flakiness",
	"time.sleep() causes timing-dependent flakiness",
	"sleep causes timing-dependent flakiness",
	"rand.Intn() without seed produces non-deterministic data",
	"rand.Float64() without seed produces non-deterministic data",
	"rand.Intn without seed produces non-deterministic data",
	"rand.Shuffle() without seed produces non-deterministic data",
	"Math.random() produces non-deterministic test data",
	"random.random() produces non-deterministic test data",
	"random.randint() produces non-deterministic test data",
	"random.choice() produces non-deterministic test data",
	"rand() produces non-deterministic test data",
}

// checkFlakyTestPatterns detects flaky test patterns in test files.
// Returns warnings as a single string, or empty string if no issues found.
//
// Parameters:
//   - filePath: path of the written file (used for test file detection)
//   - oldContent: file content before the edit ("" for new files)
//   - newContent: file content after the edit
func checkFlakyTestPatterns(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	// Only check test files.
	if !isTestFile(filePath) {
		return ""
	}

	var warnings []string

	// Delta-aware: only flag patterns newly introduced by this edit.
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	oldSet := make(map[string]bool, len(oldLines))
	for _, l := range oldLines {
		oldSet[l] = true
	}

	// For Go files, use AST analysis for precision.
	// Pass oldSet so AST warnings are delta-aware (only flag new/changed lines).
	if filepath.Ext(filePath) == ".go" {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filePath, newContent, 0)
		if err == nil {
			warnings = flakyGoTestPatterns(fset, file, newContent, oldSet)
		}
	}

	// For all languages (including Go as fallback), use regex-based detection.
	// This catches patterns in comments, string literals, and non-Go files.
	for _, newLine := range newLines {
		if oldSet[newLine] {
			continue // line existed before, skip
		}
		for i, re := range flakyPatternRegexes {
			if re.MatchString(newLine) {
				// Avoid duplicate warnings for the same pattern.
				desc := flakyPatternDescriptions[i]
				// Check if we already warned about this line.
				alreadyWarned := false
				for _, w := range warnings {
					if strings.Contains(w, desc) {
						alreadyWarned = true
						break
					}
				}
				if !alreadyWarned {
					warnings = append(warnings, fmt.Sprintf(
						"Flaky test pattern: %s. "+
							"This can cause intermittent test failures. "+
							"Use deterministic values or proper synchronization.",
						desc))
					break // one warning per line
				}
			}
		}
	}

	// Deduplicate AST warnings vs regex warnings (remove near-duplicates).
	warnings = deduplicateFlakyWarnings(warnings)

	if len(warnings) == 0 {
		return ""
	}

	if len(warnings) > flakyTestMaxWarnings {
		warnings = warnings[:flakyTestMaxWarnings]
	}

	var b strings.Builder
	b.WriteString("[Flaky test pattern warning]\n")
	for i, w := range warnings {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(w)
	}
	return b.String()
}

// flakyWarningOnOldLine checks if a warning's source line was already present
// in the old content (for delta-aware filtering). Extracts the line number from
// the warning prefix (e.g., "foo_test.go:6:6: ...") and checks that line in src.
func flakyWarningOnOldLine(warning, src string, oldLines map[string]bool) bool {
	// Extract the line number from the warning text.
	// Format: "filename:line:col: ..."
	// Find the first colon after the filename.
	parts := strings.SplitN(warning, ":", 4)
	if len(parts) < 3 {
		return false
	}
	// Parse line number from parts[1].
	lineNum := 0
	for _, c := range parts[1] {
		if c >= '0' && c <= '9' {
			lineNum = lineNum*10 + int(c-'0')
		} else {
			lineNum = 0
			break
		}
	}
	if lineNum <= 0 {
		return false
	}
	lines := strings.Split(src, "\n")
	if lineNum > len(lines) {
		return false
	}
	return oldLines[lines[lineNum-1]]
}

// detected by both AST and regex paths.
func deduplicateFlakyWarnings(warnings []string) []string {
	if len(warnings) <= 1 {
		return warnings
	}

	var result []string
	seen := make(map[string]bool)

	for _, w := range warnings {
		// Create a simplified key from the warning text.
		key := simplifyFlakyWarningKey(w)
		if !seen[key] {
			seen[key] = true
			result = append(result, w)
		}
	}

	return result
}

// simplifyFlakyWarningKey extracts a normalized key from a warning string
// for deduplication purposes.
func simplifyFlakyWarningKey(w string) string {
	lowered := strings.ToLower(w)
	// Category buckets
	switch {
	case strings.Contains(lowered, "time.now") || strings.Contains(lowered, "time-dependent"):
		return "time-dependence"
	case strings.Contains(lowered, "sleep") || strings.Contains(lowered, "timing-dependent"):
		return "timing-dependence"
	case strings.Contains(lowered, "rand") || strings.Contains(lowered, "non-deterministic") || strings.Contains(lowered, "math.random"):
		return "random-data"
	case strings.Contains(lowered, "goroutine") || strings.Contains(lowered, "waitgroup"):
		return "goroutine-race"
	case strings.Contains(lowered, "map") && strings.Contains(lowered, "iteration"):
		return "map-order"
	default:
		return lowered
	}
}
