package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

type Language int

const (
	LangAny    Language = 0
	LangGo     Language = 1
	LangPython Language = 2
	LangJSTS   Language = 3
	LangMarkup Language = 4
	LangConfig Language = 5
	LangRuby   Language = 6
	LangJava   Language = 7
)

type CheckSeverity int

const (
	SeverityDefault CheckSeverity = iota
	SeverityCritical
)

func detectLanguage(filePath string) Language {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return LangGo
	case ".py", ".pyw", ".pyi":
		return LangPython
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return LangJSTS
	case ".html", ".htm", ".xml", ".vue", ".svelte", ".svg":
		return LangMarkup
	case ".json", ".jsonc", ".yaml", ".yml", ".toml":
		return LangConfig
	case ".rb", ".ruby", ".rake", ".gemspec":
		return LangRuby
	case ".java":
		return LangJava
	default:
		return LangAny
	}
}

type CheckContext struct {
	FilePath   string
	OldContent string
	NewContent string
	Lang       Language

	GoAST  *ast.File
	GoFset *token.FileSet
}

type IntegrityCheck struct {
	Name     string
	Langs    []Language
	Severity CheckSeverity
	Run      func(ctx CheckContext) []string
}

func (c IntegrityCheck) appliesTo(lang Language) bool {
	if len(c.Langs) == 0 {
		return true
	}
	for _, l := range c.Langs {
		if l == LangAny || l == lang {
			return true
		}
	}
	return false
}

var allChecks []IntegrityCheck

func init() {
	registerAllChecks()
}

const checkTimeout = 2 * time.Second

func runChecksParallel(ctx CheckContext) []string {

	resetParseMemo()
	applicable := make([]int, 0, len(allChecks))
	for i, c := range allChecks {
		if c.appliesTo(ctx.Lang) {
			applicable = append(applicable, i)
		}
	}

	if len(applicable) == 0 {
		return nil
	}

	type result struct {
		index    int
		severity CheckSeverity
		warnings []string
	}

	var mu sync.Mutex
	results := make([]result, 0, len(applicable))
	var wg sync.WaitGroup

	const maxWorkers = 8
	sem := make(chan struct{}, maxWorkers)

	for _, idx := range applicable {
		wg.Add(1)
		sem <- struct{}{}
		go func(checkIdx int) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					debug.Log("integrity", "check %q panicked: %v", allChecks[checkIdx].Name, r)
				}
			}()

			done := make(chan []string, 1)
			go func() {
				var warnings []string
				defer func() {
					if r := recover(); r != nil {
						debug.Log("integrity", "check %q panicked: %v", allChecks[checkIdx].Name, r)
						warnings = nil
					}

					done <- warnings
				}()
				warnings = allChecks[checkIdx].Run(ctx)
			}()

			var warnings []string
			select {
			case warnings = <-done:
			case <-time.After(checkTimeout):
				debug.Log("integrity", "check %q timed out after %v and was skipped", allChecks[checkIdx].Name, checkTimeout)
				warnings = nil
			}

			if len(warnings) > 0 {
				mu.Lock()
				results = append(results, result{index: checkIdx, severity: allChecks[checkIdx].Severity, warnings: warnings})
				mu.Unlock()
			}
		}(idx)
	}

	wg.Wait()

	sort.Slice(results, func(a, b int) bool {
		if results[a].severity != results[b].severity {
			return results[a].severity > results[b].severity
		}
		return results[a].index < results[b].index
	})

	var merged []string
	for _, r := range results {
		merged = append(merged, r.warnings...)
	}
	return merged
}

func formatWarnings(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}

	for _, w := range warnings {
		debug.Log("integrity", "%s", w)
	}

	if len(warnings) > maxIntegrityWarnings {
		suppressed := len(warnings) - maxIntegrityWarnings
		warnings = warnings[:maxIntegrityWarnings]

		warnings = append(warnings, fmt.Sprintf("... and %d more integrity warning(s) suppressed (see debug log: integrity)", suppressed))
	}

	var b strings.Builder
	b.WriteString("[Post-write integrity check]\n")
	for i, w := range warnings {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(w)
	}
	return b.String()
}

func newCheckContext(filePath, oldContent, newContent string) CheckContext {
	ctx := CheckContext{
		FilePath:   filePath,
		OldContent: oldContent,
		NewContent: newContent,
		Lang:       detectLanguage(filePath),
	}

	if ctx.Lang == LangGo && strings.TrimSpace(newContent) != "" {
		fset := token.NewFileSet()
		goAST, err := parser.ParseFile(fset, filePath, newContent, 0)
		if err == nil {
			ctx.GoFset = fset
			ctx.GoAST = goAST
		}
	}

	return ctx
}
