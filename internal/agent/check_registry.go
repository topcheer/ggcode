package agent

import (
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

// Check Registry: a language-aware, parallel-execution framework for
// post-write integrity checks.
//
// Design goals:
//  1. Language awareness: Go-only checks are skipped for non-Go files,
//     eliminating wasted CPU on irrelevant analysis.
//  2. Parallel execution: independent checks run concurrently, bounded
//     by a semaphore to avoid goroutine explosion.
//  3. Fault isolation: panics in individual checks are recovered and
//     logged, never crashing the pipeline or the agent loop.
//  4. External command safety: checks that depend on external tools
//     (git, go vet, etc.) must handle missing commands gracefully and
//     must NOT inject tool errors into the integrity result.

// Language represents a programming or markup language for check filtering.
type Language int

const (
	LangAny    Language = 0
	LangGo     Language = 1
	LangPython Language = 2
	LangJSTS   Language = 3 // .js, .ts, .jsx, .tsx, .mjs, .cjs
	LangMarkup Language = 4 // .html, .xml, .vue, .svelte, .svg
	LangConfig Language = 5 // .json, .yaml, .yml, .toml
	LangRuby   Language = 6 // .rb, .ruby
	LangJava   Language = 7 // .java
)

// CheckSeverity ranks how critical a check's findings are. When the
// maxIntegrityWarnings budget forces the registry to choose which single
// warning surfaces, higher-severity findings win regardless of
// registration order; equal severities fall back to registration order.
type CheckSeverity int

const (
	// SeverityDefault is the zero value: correctness findings that matter
	// but do not outrank other findings by class.
	SeverityDefault CheckSeverity = iota
	// SeverityCritical marks checks whose findings indicate a security
	// vulnerability or guaranteed corruption/crash (injected secrets, SQL
	// injection, broken syntax, data-destroying writes). #601 W3: before
	// this field existed the cap truncated strictly by registration index,
	// so an early-registered advisory (e.g. edit-blast-radius) could crowd
	// out a late-registered sql-injection finding.
	SeverityCritical
)

// detectLanguage infers the Language from the file extension.
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

// CheckContext provides shared, pre-computed context for all checks.
type CheckContext struct {
	FilePath   string
	OldContent string
	NewContent string
	Lang       Language

	// Go-specific: pre-parsed AST shared by all Go checks to avoid
	// redundant parser.ParseFile calls. nil if not Go or parse failed.
	GoAST  *ast.File
	GoFset *token.FileSet
}

// IntegrityCheck represents a single post-write integrity check.
type IntegrityCheck struct {
	Name     string
	Langs    []Language // empty or contains LangAny => runs for all languages
	Severity CheckSeverity
	Run      func(ctx CheckContext) []string
}

// appliesTo returns true if the check should run for the given language.
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

// allChecks is the registered set of integrity checks, populated by init().
var allChecks []IntegrityCheck

func init() {
	registerAllChecks()
}

// checkTimeout bounds how long a single integrity check may run before the
// registry gives up on it and moves on. Most checks finish in microseconds;
// this budget only matters when a check hangs (pathological input, blocked
// external command). Without it, one wedged check would stall wg.Wait()
// forever and freeze the write tool. A timed-out check is logged and skipped —
// its goroutine may linger, but the write pipeline always completes.
const checkTimeout = 2 * time.Second

// runChecksParallel executes all applicable checks concurrently with panic
// recovery. Returns warnings sorted by registration order for deterministic
// output.
func runChecksParallel(ctx CheckContext) []string {
	// Start each check run with a clean parse memo: entries must never
	// outlive the write that produced them.
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

			// Run the check under a timeout so a hung check cannot stall
			// the whole registry: wait for its result or give up after
			// checkTimeout and let the write proceed.
			done := make(chan []string, 1)
			go func() {
				var warnings []string
				defer func() {
					if r := recover(); r != nil {
						debug.Log("integrity", "check %q panicked: %v", allChecks[checkIdx].Name, r)
						warnings = nil
					}
					// Always send (buffered) so a panic resolves the
					// select immediately instead of waiting out the
					// timeout.
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

	// #601 W3: order by severity first (most critical surfaces when the
	// maxIntegrityWarnings cap truncates), then by registration index for
	// stable, deterministic output within the same severity tier.
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

// formatWarnings renders warnings into the legacy string format expected by
// callers, respecting the maxIntegrityWarnings cap. Callers receive the
// warnings already ordered by severity (see runChecksParallel), so the cap
// keeps the single most critical issue — not merely the earliest-registered
// one (#601 W3).
func formatWarnings(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}

	// Log ALL warnings for offline analysis.
	for _, w := range warnings {
		debug.Log("integrity", "%s", w)
	}

	if len(warnings) > maxIntegrityWarnings {
		warnings = warnings[:maxIntegrityWarnings]
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

// newCheckContext builds a CheckContext, pre-parsing the Go AST if applicable.
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
