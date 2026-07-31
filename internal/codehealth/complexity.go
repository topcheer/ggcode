// Package codehealth provides static analysis of Go source code to measure
// code quality metrics such as cyclomatic complexity, function length, and
// nesting depth. It is designed to be used by the AI agent as a tool to
// proactively identify technical debt hotspots and suggest refactoring.
package codehealth

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Severity levels for functions exceeding thresholds.
type Severity string

const (
	SeverityCritical Severity = "critical" // complexity > 30
	SeverityHigh     Severity = "high"     // complexity 20-30
	SeverityMedium   Severity = "medium"   // complexity 11-19
	SeverityLow      Severity = "low"      // complexity <= 10 (healthy)
)

// FuncMetrics holds quality metrics for a single Go function.
type FuncMetrics struct {
	File         string   `json:"file"`
	Function     string   `json:"function"`
	Line         int      `json:"line"`
	EndLine      int      `json:"end_line"`
	Complexity   int      `json:"complexity"`    // cyclomatic complexity
	Length       int      `json:"length"`        // lines of code (body)
	NestingDepth int      `json:"nesting_depth"` // max nesting level
	Params       int      `json:"params"`        // parameter count
	Statements   int      `json:"statements"`    // total statement count
	Severity     Severity `json:"severity"`
}

// FileSummary holds aggregated metrics for a single file.
type FileSummary struct {
	File            string  `json:"file"`
	Functions       int     `json:"functions"`
	MaxComplexity   int     `json:"max_complexity"`
	AvgComplexity   float64 `json:"avg_complexity"`
	TotalStatements int     `json:"total_statements"`
}

// Report is the top-level analysis result.
type Report struct {
	Path          string        `json:"path"`
	FilesScanned  int           `json:"files_scanned"`
	Functions     int           `json:"functions_analyzed"`
	AvgComplexity float64       `json:"avg_complexity"`
	MaxComplexity int           `json:"max_complexity"`
	TopFunctions  []FuncMetrics `json:"top_functions"`  // sorted by complexity desc
	FileSummaries []FileSummary `json:"file_summaries"` // sorted by max complexity desc
	HealthScore   int           `json:"health_score"`   // 0-100, higher is better
}

// Options controls analysis behavior.
type Options struct {
	// MaxFiles limits the number of files to scan (0 = no limit).
	MaxFiles int
	// MaxFunctions limits the number of functions in TopFunctions (default 20).
	MaxFunctions int
	// ThresholdComplexity is the minimum complexity to flag (default 11).
	ThresholdComplexity int
	// ExcludeDirs are directory names to skip (e.g. vendor, testdata).
	ExcludeDirs []string
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{
		MaxFunctions:        20,
		ThresholdComplexity: 11,
		ExcludeDirs:         []string{"vendor", "testdata", "node_modules", ".git", "third_party"},
	}
}

// Analyze walks a directory and analyzes all .go files for code quality metrics.
func Analyze(dir string, opts Options) (*Report, error) {
	if opts.MaxFunctions <= 0 {
		opts.MaxFunctions = 20
	}
	if opts.ThresholdComplexity <= 0 {
		opts.ThresholdComplexity = 11
	}
	if len(opts.ExcludeDirs) == 0 {
		opts.ExcludeDirs = []string{"vendor", "testdata", "node_modules", ".git"}
	}

	dir = filepath.Clean(dir)
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot access path %q: %w", dir, err)
	}

	var allFuncs []FuncMetrics
	var fileSummaries []FileSummary
	filesScanned := 0

	if !info.IsDir() {
		// Single file analysis
		funcs, err := analyzeFile(dir)
		if err != nil {
			return nil, err
		}
		allFuncs = append(allFuncs, funcs...)
		filesScanned = 1
		if len(funcs) > 0 {
			fileSummaries = append(fileSummaries, summarizeFile(dir, funcs))
		}
	} else {
		excludeSet := make(map[string]bool)
		for _, d := range opts.ExcludeDirs {
			excludeSet[d] = true
		}

		err = filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil // skip unreadable paths
			}
			if fi.IsDir() {
				name := filepath.Base(path)
				if excludeSet[name] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			// Skip generated files
			if isGenerated(path) {
				return nil
			}
			if opts.MaxFiles > 0 && filesScanned >= opts.MaxFiles {
				return filepath.SkipDir
			}

			funcs, err := analyzeFile(path)
			if err != nil {
				return nil // skip unparseable files
			}
			allFuncs = append(allFuncs, funcs...)
			filesScanned++
			if len(funcs) > 0 {
				fileSummaries = append(fileSummaries, summarizeFile(path, funcs))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return buildReport(dir, allFuncs, fileSummaries, filesScanned, opts), nil
}

// analyzeFile parses a single Go file and extracts function metrics.
func analyzeFile(path string) ([]FuncMetrics, error) {
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var results []FuncMetrics

	for _, decl := range src.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		// Skip functions with empty bodies (interfaces, stubs)
		if fn.Body == nil {
			continue
		}

		startPos := fset.Position(fn.Pos())
		endPos := fset.Position(fn.End())
		name := funcName(fn)

		metrics := FuncMetrics{
			File:       path,
			Function:   name,
			Line:       startPos.Line,
			EndLine:    endPos.Line,
			Length:     endPos.Line - startPos.Line,
			Params:     paramCount(fn),
			Complexity: cyclomaticComplexity(fn),
			Statements: countStatements(fn),
		}
		metrics.NestingDepth = maxNestingDepth(fn.Body, 0)
		metrics.Severity = classifySeverity(metrics.Complexity)

		results = append(results, metrics)
	}

	return results, nil
}

// cyclomaticComplexity calculates McCabe's cyclomatic complexity.
// Starts at 1, +1 for each decision point: if, for, range, case, &&, ||, etc.
func cyclomaticComplexity(fn *ast.FuncDecl) int {
	v := 1
	ast.Inspect(fn, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			v++
		case *ast.CaseClause:
			// 'case' in switch adds a path. 'default' doesn't add complexity.
			if n.List != nil {
				v++
			}
		case *ast.CommClause:
			// select case with communication
			if n.Comm != nil {
				v++
			}
		case *ast.BinaryExpr:
			// && and || add decision branches
			if n.Op == token.LAND || n.Op == token.LOR {
				v++
			}
		}
		return true
	})
	return v
}

// maxNestingDepth finds the deepest nesting level in a function body.
func maxNestingDepth(body *ast.BlockStmt, current int) int {
	max := current
	for _, stmt := range body.List {
		depth := nestingDepthOfStmt(stmt, current)
		if depth > max {
			max = depth
		}
	}
	return max
}

func nestingDepthOfStmt(stmt ast.Stmt, current int) int {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		depth := current + 1
		if d := maxNestingDepth(s.Body, depth); d > current {
			return d
		}
		// else branch
		if s.Else != nil {
			if d := nestingDepthOfStmt(s.Else, current); d > current {
				return d
			}
		}
		return current + 1
	case *ast.ForStmt:
		return maxOf(current+1, maxNestingDepth(s.Body, current+1))
	case *ast.RangeStmt:
		return maxOf(current+1, maxNestingDepth(s.Body, current+1))
	case *ast.SwitchStmt:
		return maxOf(current+1, maxNestingDepth(s.Body, current+1))
	case *ast.TypeSwitchStmt:
		return maxOf(current+1, maxNestingDepth(s.Body, current+1))
	case *ast.SelectStmt:
		return maxOf(current+1, maxNestingDepth(s.Body, current+1))
	case *ast.BlockStmt:
		return maxNestingDepth(s, current)
	}
	return current
}

// countStatements counts all executable statements in a function.
func countStatements(fn *ast.FuncDecl) int {
	count := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		if _, ok := n.(ast.Stmt); ok {
			// Don't count the function body itself as a statement
			if bl, ok := n.(*ast.BlockStmt); ok && bl == fn.Body {
				return true
			}
			count++
		}
		return true
	})
	return count
}

func paramCount(fn *ast.FuncDecl) int {
	if fn.Type == nil || fn.Type.Params == nil {
		return 0
	}
	count := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			count++ // unnamed parameter
		} else {
			count += len(field.Names)
		}
	}
	return count
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		// Method: ReceiverType.MethodName
		recvType := ""
		switch t := fn.Recv.List[0].Type.(type) {
		case *ast.Ident:
			recvType = t.Name
		case *ast.StarExpr:
			if id, ok := t.X.(*ast.Ident); ok {
				recvType = "*" + id.Name
			}
		}
		if recvType != "" {
			return recvType + "." + fn.Name.Name
		}
	}
	return fn.Name.Name
}

func classifySeverity(complexity int) Severity {
	switch {
	case complexity > 30:
		return SeverityCritical
	case complexity >= 20:
		return SeverityHigh
	case complexity >= 11:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

func summarizeFile(path string, funcs []FuncMetrics) FileSummary {
	s := FileSummary{File: path, Functions: len(funcs)}
	if len(funcs) == 0 {
		return s
	}
	totalC := 0
	for _, f := range funcs {
		if f.Complexity > s.MaxComplexity {
			s.MaxComplexity = f.Complexity
		}
		totalC += f.Complexity
		s.TotalStatements += f.Statements
	}
	s.AvgComplexity = float64(totalC) / float64(len(funcs))
	return s
}

func buildReport(path string, funcs []FuncMetrics, files []FileSummary, filesScanned int, opts Options) *Report {
	r := &Report{
		Path:         path,
		FilesScanned: filesScanned,
		Functions:    len(funcs),
	}

	if len(funcs) == 0 {
		r.HealthScore = 100
		return r
	}

	totalC := 0
	for _, f := range funcs {
		totalC += f.Complexity
		if f.Complexity > r.MaxComplexity {
			r.MaxComplexity = f.Complexity
		}
	}
	r.AvgComplexity = float64(totalC) / float64(len(funcs))

	// Sort functions by complexity descending
	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].Complexity > funcs[j].Complexity
	})

	// Filter to threshold and limit
	var flagged []FuncMetrics
	for _, f := range funcs {
		if f.Complexity >= opts.ThresholdComplexity {
			flagged = append(flagged, f)
		}
	}
	if len(flagged) > opts.MaxFunctions {
		flagged = flagged[:opts.MaxFunctions]
	}
	r.TopFunctions = flagged

	// Sort file summaries by max complexity descending
	sort.Slice(files, func(i, j int) bool {
		return files[i].MaxComplexity > files[j].MaxComplexity
	})
	if len(files) > 10 {
		files = files[:10]
	}
	r.FileSummaries = files

	// Health score: 100 - penalty. Penalty based on ratio of flagged functions.
	flaggedCount := 0
	for _, f := range funcs {
		if f.Complexity >= opts.ThresholdComplexity {
			flaggedCount++
		}
	}
	ratio := float64(flaggedCount) / float64(len(funcs))
	penalty := int(ratio * 100)
	// Extra penalty for critical/high severity
	for _, f := range funcs {
		switch f.Severity {
		case SeverityCritical:
			penalty += 2
		case SeverityHigh:
			penalty += 1
		}
	}
	r.HealthScore = 100 - penalty
	if r.HealthScore < 0 {
		r.HealthScore = 0
	}

	return r
}

// isGenerated checks if a Go file is auto-generated.
func isGenerated(path string) bool {
	// Fast path: check filename
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_gen.go") || strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_string.go") {
		return true
	}
	// Check first line for "Code generated" comment
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	// Only read first 200 bytes for performance
	end := 200
	if len(data) < end {
		end = len(data)
	}
	prefix := string(data[:end])
	return strings.Contains(prefix, "Code generated")
}

func maxOf(a, b int) int {
	if a > b {
		return a
	}
	return b
}
