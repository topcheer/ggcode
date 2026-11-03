package codehealth

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

type FuncMetrics struct {
	File         string   `json:"file"`
	Function     string   `json:"function"`
	Line         int      `json:"line"`
	EndLine      int      `json:"end_line"`
	Complexity   int      `json:"complexity"`
	Length       int      `json:"length"`
	NestingDepth int      `json:"nesting_depth"`
	Params       int      `json:"params"`
	Statements   int      `json:"statements"`
	Severity     Severity `json:"severity"`
}

type FileSummary struct {
	File            string  `json:"file"`
	Functions       int     `json:"functions"`
	MaxComplexity   int     `json:"max_complexity"`
	AvgComplexity   float64 `json:"avg_complexity"`
	TotalStatements int     `json:"total_statements"`
}

type Report struct {
	Path          string        `json:"path"`
	FilesScanned  int           `json:"files_scanned"`
	Functions     int           `json:"functions_analyzed"`
	AvgComplexity float64       `json:"avg_complexity"`
	MaxComplexity int           `json:"max_complexity"`
	TopFunctions  []FuncMetrics `json:"top_functions"`
	FileSummaries []FileSummary `json:"file_summaries"`
	HealthScore   int           `json:"health_score"`
}

type Options struct {
	MaxFiles            int
	MaxFunctions        int
	ThresholdComplexity int
	ExcludeDirs         []string
}

func DefaultOptions() Options {
	return Options{
		MaxFunctions:        20,
		ThresholdComplexity: 11,
		ExcludeDirs:         []string{"vendor", "testdata", "node_modules", ".git", "third_party"},
	}
}

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

	if !info.IsDir() {
		return analyzeSingleFile(dir, opts)
	}

	var allFuncs []FuncMetrics
	var fileSummaries []FileSummary
	filesScanned := 0

	excludeSet := make(map[string]bool)
	for _, d := range opts.ExcludeDirs {
		excludeSet[d] = true
	}

	err = filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil
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
		if isGenerated(path) {
			return nil
		}
		if opts.MaxFiles > 0 && filesScanned >= opts.MaxFiles {
			return filepath.SkipAll
		}

		funcs, err := analyzeFile(path)
		if err != nil {
			return nil
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

	return buildReport(dir, allFuncs, fileSummaries, filesScanned, opts), nil
}

func analyzeSingleFile(path string, opts Options) (*Report, error) {
	if isGenerated(path) {
		return buildReport(path, nil, nil, 0, opts), nil
	}
	funcs, err := analyzeFile(path)
	if err != nil {
		return nil, err
	}
	var fileSummaries []FileSummary
	if len(funcs) > 0 {
		fileSummaries = append(fileSummaries, summarizeFile(path, funcs))
	}
	return buildReport(path, funcs, fileSummaries, 1, opts), nil
}

func analyzeFile(path string) ([]FuncMetrics, error) {
	return analyzeSource(path, nil)
}

func analyzeSource(filename string, src []byte) ([]FuncMetrics, error) {
	if src == nil {
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		src = data
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var results []FuncMetrics

	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		if fn.Body == nil {
			continue
		}

		startPos := fset.Position(fn.Pos())
		endPos := fset.Position(fn.End())
		name := funcName(fn)

		metrics := FuncMetrics{
			File:       filename,
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

func cyclomaticComplexity(fn *ast.FuncDecl) int {
	v := 1
	ast.Inspect(fn, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			v++
		case *ast.CaseClause:
			if n.List != nil {
				v++
			}
		case *ast.CommClause:
			if n.Comm != nil {
				v++
			}
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				v++
			}
		}
		return true
	})
	return v
}

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

func countStatements(fn *ast.FuncDecl) int {
	count := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		if _, ok := n.(ast.Stmt); ok {
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
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
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

	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].Complexity > funcs[j].Complexity
	})

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

	sort.Slice(files, func(i, j int) bool {
		return files[i].MaxComplexity > files[j].MaxComplexity
	})
	if len(files) > 10 {
		files = files[:10]
	}
	r.FileSummaries = files

	flaggedCount := 0
	for _, f := range funcs {
		if f.Complexity >= opts.ThresholdComplexity {
			flaggedCount++
		}
	}
	ratio := float64(flaggedCount) / float64(len(funcs))
	penalty := int(ratio * 100)
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

func AnalyzeSource(filename string, src []byte, opts Options) (*Report, error) {
	if opts.MaxFunctions <= 0 {
		opts.MaxFunctions = 20
	}
	if opts.ThresholdComplexity <= 0 {
		opts.ThresholdComplexity = 11
	}
	if len(opts.ExcludeDirs) == 0 {
		opts.ExcludeDirs = []string{"vendor", "testdata", ".git"}
	}
	funcs, err := analyzeSource(filename, src)
	if err != nil {
		return nil, err
	}
	var fileSummaries []FileSummary
	if len(funcs) > 0 {
		fileSummaries = append(fileSummaries, summarizeFile(filename, funcs))
	}
	return buildReport(filename, funcs, fileSummaries, 1, opts), nil
}

func IsGenerated(path string) bool { return isGenerated(path) }

func isGenerated(path string) bool {
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_gen.go") || strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_string.go") {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 200)
	n, _ := io.ReadFull(f, buf)
	if n == 0 {
		return false
	}
	prefix := string(buf[:n])
	return strings.Contains(prefix, "Code generated")
}

func maxOf(a, b int) int {
	if a > b {
		return a
	}
	return b
}
