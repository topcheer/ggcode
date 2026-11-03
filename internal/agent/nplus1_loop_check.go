package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

var ioExactPatterns = map[string]string{

	"http.Get":  "HTTP request",
	"http.Post": "HTTP request",
	"http.Head": "HTTP request",

	"os.ReadFile":      "file read",
	"os.WriteFile":     "file write",
	"ioutil.ReadFile":  "file read",
	"ioutil.WriteFile": "file write",
	"os.Open":          "file open",
}

var ioPreciseMethodPatterns = map[string]string{
	".Query":           "database query",
	".QueryRow":        "database query",
	".QueryContext":    "database query",
	".QueryRowContext": "database query",
	".Exec":            "database exec",
	".ExecContext":     "database exec",
	".GetWithContext":  "HTTP request",
}

var ioGenericMethodPatterns = map[string]string{
	".Get":    "database Get",
	".Select": "database Select",
	".Find":   "database Find",
	".First":  "database First",
	".Where":  "database Where (GORM)",
	".Create": "database Create (GORM)",
	".Update": "database Update (GORM)",
	".Delete": "database Delete (GORM)",
	".Save":   "database Save (GORM)",
	".Do":     "HTTP request (client.Do)",
	".HGet":   "Redis HGet",
	".HSet":   "Redis HSet",
	".HMGet":  "Redis HMGet",
	".HMSet":  "Redis HMSet",
}

var ioReceiverSignals = []string{
	"db", "sql", "gorm", "xorm", "mongo", "redis", "http", "conn", "coll",
}

func checkNPlus1Loop(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newAST, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil {
		return nil
	}

	var oldAST *ast.File
	if strings.TrimSpace(oldContent) != "" {
		oldAST, _ = parser.ParseFile(token.NewFileSet(), filePath, oldContent, 0)
	}

	newPatterns := findIOInLoops(newAST)
	if len(newPatterns) == 0 {
		return nil
	}

	if oldAST != nil {
		budget := make(map[string]int)
		for _, p := range newPatterns {
			budget[p.String()]++
		}
		for _, p := range findIOInLoops(oldAST) {
			if c := budget[p.String()]; c > 0 {
				budget[p.String()] = c - 1
			}
		}
		var delta []ioLoopPattern
		for _, p := range newPatterns {
			if budget[p.String()] > 0 {
				budget[p.String()]--
				delta = append(delta, p)
			}
		}
		newPatterns = delta
	}

	if len(newPatterns) == 0 {
		return nil
	}

	var warnings []string
	for i, p := range newPatterns {
		if i >= 2 {
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"N+1 I/O anti-pattern: %s call inside loop at %s:%d. "+
				"Each iteration triggers a separate I/O round-trip (N iterations = N calls). "+
				"Consider batching: use a single query with WHERE IN, bulk insert, or collect IDs first then fetch in one call.",
			p.ioType, filepath.Base(filePath), fset.Position(p.pos).Line))
	}

	if extra := len(newPatterns) - 2; extra > 0 {
		warnings = append(warnings, fmt.Sprintf("...and %d more N+1 I/O pattern(s) in %s", extra, filepath.Base(filePath)))
	}

	return warnings
}

type ioLoopPattern struct {
	pos    token.Pos
	ioType string
	key    string
}

func (p ioLoopPattern) String() string {
	return p.key
}

type loopSite struct {
	body   *ast.BlockStmt
	fnName string
}

func findIOInLoops(file *ast.File) []ioLoopPattern {
	var sites []loopSite

	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.ForStmt:
			if v.Body != nil {
				sites = append(sites, loopSite{body: v.Body})
			}
		case *ast.RangeStmt:
			if v.Body != nil {
				sites = append(sites, loopSite{body: v.Body})
			}
		}
		return true
	})

	patterns := make([]ioLoopPattern, 0, len(sites))
	visited := make(map[*ast.CallExpr]bool)

	for _, site := range sites {
		site.fnName = ioEnclosingFuncName(file, site.body.Pos())
		ast.Inspect(site.body, func(inner ast.Node) bool {

			if _, isFuncLit := inner.(*ast.FuncLit); isFuncLit {
				return false
			}
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			if visited[call] {
				return true
			}
			visited[call] = true

			ioType := identifyIOCall(call)
			if ioType != "" {
				patterns = append(patterns, ioLoopPattern{
					pos:    call.Pos(),
					ioType: ioType,
					key: fmt.Sprintf("%s|%s|%s", ioType, site.fnName,
						ioNormalizeCallText(call)),
				})
			}
			return true
		})
	}

	return patterns
}

func ioEnclosingFuncName(file *ast.File, pos token.Pos) string {
	name := "_"
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Pos() <= pos && pos <= fn.End() {
			name = fn.Name.Name
			break
		}
	}
	return name
}

func identifyIOCall(call *ast.CallExpr) string {
	name := callFuncName(call)
	if name == "" {
		return ""
	}

	if desc, ok := ioExactPatterns[name]; ok {
		return desc
	}

	if _, isSelector := call.Fun.(*ast.SelectorExpr); !isSelector {
		return ""
	}

	for suffix, desc := range ioPreciseMethodPatterns {
		if strings.HasSuffix(name, suffix) {
			return desc
		}
	}

	for suffix, desc := range ioGenericMethodPatterns {
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		if ioHasReceiverSignal(call.Fun.(*ast.SelectorExpr).X) {
			return desc
		}
	}

	return ""
}

func ioHasReceiverSignal(recv ast.Expr) bool {
	name := ioReceiverIdentifier(recv)
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	for _, sig := range ioReceiverSignals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

func ioReceiverIdentifier(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.ParenExpr:
		return ioReceiverIdentifier(v.X)
	case *ast.StarExpr:
		return ioReceiverIdentifier(v.X)
	case *ast.SelectorExpr:
		return v.Sel.Name
	default:
		return ""
	}
}

func callFuncName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		parts := make([]string, 0, 4)
		ioAppendSelectorParts(&parts, fn)
		return strings.Join(parts, ".")
	default:
		return ""
	}
}

func ioAppendSelectorParts(parts *[]string, expr *ast.SelectorExpr) {
	switch x := expr.X.(type) {
	case *ast.Ident:
		*parts = append(*parts, x.Name)
	case *ast.SelectorExpr:
		ioAppendSelectorParts(parts, x)
	}
	*parts = append(*parts, expr.Sel.Name)
}

func ioNormalizeCallText(call *ast.CallExpr) string {
	var b strings.Builder
	ioAppendNormalized(&b, call)
	return b.String()
}

func ioAppendNormalized(b *strings.Builder, n ast.Node) {
	switch v := n.(type) {
	case *ast.Ident:
		b.WriteString(v.Name)
	case *ast.BasicLit:
		b.WriteString(v.Kind.String())
		b.WriteByte(':')
		b.WriteString(v.Value)
	case *ast.SelectorExpr:
		ioAppendNormalized(b, v.X)
		b.WriteByte('.')
		b.WriteString(v.Sel.Name)
	case *ast.ParenExpr:
		b.WriteByte('(')
		ioAppendNormalized(b, v.X)
		b.WriteByte(')')
	case *ast.StarExpr:
		b.WriteByte('*')
		ioAppendNormalized(b, v.X)
	case *ast.UnaryExpr:
		b.WriteString(v.Op.String())
		ioAppendNormalized(b, v.X)
	case *ast.BinaryExpr:
		ioAppendNormalized(b, v.X)
		b.WriteString(v.Op.String())
		ioAppendNormalized(b, v.Y)
	case *ast.IndexExpr:
		ioAppendNormalized(b, v.X)
		b.WriteByte('[')
		ioAppendNormalized(b, v.Index)
		b.WriteByte(']')
	case *ast.KeyValueExpr:
		ioAppendNormalized(b, v.Key)
		b.WriteByte(':')
		ioAppendNormalized(b, v.Value)
	case *ast.Ellipsis:
		b.WriteString("...")
		if v.Elt != nil {
			ioAppendNormalized(b, v.Elt)
		}
	case *ast.CompositeLit:
		b.WriteString("composite{}")
	case *ast.CallExpr:
		ioAppendNormalized(b, v.Fun)
		b.WriteByte('(')
		for i, arg := range v.Args {
			if i > 0 {
				b.WriteByte(',')
			}
			ioAppendNormalized(b, arg)
		}
		if v.Ellipsis != token.NoPos {
			b.WriteString("...")
		}
		b.WriteByte(')')
	default:
		fmt.Fprintf(b, "<%T>", n)
	}
}
