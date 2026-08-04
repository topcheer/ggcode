package agent

// Cross-File Impact Analysis - pre-completion gate that detects when the
// agent's edits may have broken references in files it did NOT edit.
//
// Problem: When the agent edits a Go file (e.g., renames a function, removes
// a type, changes a function signature, deletes a method), other files in the
// package or module that reference that symbol will fail to compile. The agent
// typically only edits the files it was asked to change and may not realize
// that its modifications have cascading effects on files it never opened.
//
// The existing write-integrity checks (check_registry.go) validate the
// SYNTAX of individual files after each edit. The change reconciliation gate
// (change_reconcile.go) detects unexpected side-effect files. But NEITHER
// detects semantic breakage in OTHER files caused by the agent's edits.
//
// This gate fills that gap by:
//  1. Parsing the DIFF of each Go file the agent edited (old vs new content)
//  2. Extracting exported symbols that were REMOVED or RENAMED
//  3. Scanning sibling Go files (same package directory) for references to
//     those removed symbols
//  4. Warning the agent about files that may need updates
//
// Competitor analysis:
//   - Claude Code: relies on `go build` to catch these, reactive not proactive
//   - Cursor: IDE diagnostics flag broken references in real-time
//   - Cline/OpenHands: no cross-file impact analysis; relies on build errors
//   - Aider: uses tree-sitter to track function/class changes across files
//   - Devin: has a dependency graph but doesn't pre-warn about breakage
//
// Key design decisions:
//   - Go-only (other languages lack reliable static analysis without LSP)
//   - Uses go/ast for precise symbol extraction (function/method/type/var)
//   - Scans same-directory siblings only (most common breakage pattern)
//   - Runs at most once per run, before the change reconciliation gate
//   - Advisory: injects context, doesn't block completion
//   - Zero LLM cost - pure static analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// crossFileImpactState tracks whether the impact analysis gate has fired.
type crossFileImpactState struct {
	fired bool
	mu    sync.Mutex
}

func newCrossFileImpactState() *crossFileImpactState {
	return &crossFileImpactState{}
}

func (c *crossFileImpactState) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fired = false
}

// maxImpactFiles caps the number of potentially-affected files listed.
const maxImpactFiles = 8

// maxScanFiles limits how many sibling files to scan per edited file.
const maxScanFiles = 30

// impactScanTimeout bounds the total analysis to prevent slowdowns in huge repos.
const impactScanTimeout = 5 * time.Second

// impactRemovedSymbol represents a Go symbol that existed in the old content
// but not in the new content of an edited file.
type impactRemovedSymbol struct {
	name     string // symbol identifier (e.g., "FooBar", "(*T).Method")
	category string // "func", "type", "method", "var", "const"
}

// fileImpact records a single edited file's removed symbols and the sibling
// files that reference them.
type fileImpact struct {
	editedFile    string
	removedSyms   []impactRemovedSymbol
	affectedFiles []string
}

// checkCrossFileImpact analyzes the agent's edited Go files for removed
// exported symbols and warns about sibling files that reference them.
//
// The gate fires at most once per run. It is advisory - it injects context
// to help the agent proactively fix dependent files, but doesn't block.
func (a *Agent) checkCrossFileImpact(runStats *RunStats) string {
	if a.crossFileImpact == nil {
		return ""
	}
	a.crossFileImpact.mu.Lock()
	if a.crossFileImpact.fired {
		a.crossFileImpact.mu.Unlock()
		return ""
	}
	a.crossFileImpact.fired = true
	a.crossFileImpact.mu.Unlock()

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}

	// Collect edited Go files.
	var goFiles []string
	for _, f := range runStats.FilesEdited {
		if filepath.Ext(f) == ".go" {
			goFiles = append(goFiles, f)
		}
	}
	if len(goFiles) == 0 {
		return ""
	}

	if len(goFiles) > 20 {
		debug.Log("cross_impact", "skipping: %d Go files edited (too many)", len(goFiles))
		return ""
	}

	deadline := time.Now().Add(impactScanTimeout)
	var impacts []fileImpact

	for _, editedFile := range goFiles {
		if time.Now().After(deadline) {
			debug.Log("cross_impact", "analysis timed out after %v", impactScanTimeout)
			break
		}

		absPath := editedFile
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(workingDir, editedFile)
		}

		newContent, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}

		oldContent, err := gitFileContentAtHEAD(workingDir, editedFile)
		if err != nil || oldContent == "" {
			continue
		}

		removed := extractImpactRemovedSymbols(oldContent, string(newContent), absPath)
		if len(removed) == 0 {
			continue
		}

		dir := filepath.Dir(absPath)
		siblings := findSiblingGoFiles(dir, absPath, maxScanFiles)
		if len(siblings) == 0 {
			continue
		}

		affectedSet := make(map[string]bool)
		for _, sibling := range siblings {
			if time.Now().After(deadline) {
				break
			}
			content, err := os.ReadFile(sibling)
			if err != nil {
				continue
			}
			if referencesAnyImpactSymbol(string(content), removed) {
				relPath, _ := filepath.Rel(workingDir, sibling)
				if relPath == "" {
					relPath = sibling
				}
				affectedSet[relPath] = true
			}
		}

		if len(affectedSet) > 0 {
			var affected []string
			for f := range affectedSet {
				affected = append(affected, f)
			}
			impacts = append(impacts, fileImpact{
				editedFile:    editedFile,
				removedSyms:   removed,
				affectedFiles: affected,
			})
		}
	}

	if len(impacts) == 0 {
		return ""
	}

	totalAffected := 0
	totalRemoved := 0
	for _, imp := range impacts {
		totalAffected += len(imp.affectedFiles)
		totalRemoved += len(imp.removedSyms)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"[cross-file impact analysis] Your edits removed or renamed %d exported symbol(s) "+
			"that are referenced by %d file(s) you did NOT edit. These files may fail "+
			"to compile. Update them to fix the broken references:\n\n",
		totalRemoved, totalAffected,
	))

	shown := 0
	for _, imp := range impacts {
		if shown >= maxImpactFiles {
			break
		}
		var symNames []string
		for _, s := range imp.removedSyms {
			symNames = append(symNames, fmt.Sprintf("%s (%s)", s.name, s.category))
		}
		b.WriteString(fmt.Sprintf("  %s: removed %s\n", imp.editedFile, strings.Join(symNames, ", ")))
		for _, af := range imp.affectedFiles {
			if shown >= maxImpactFiles {
				break
			}
			b.WriteString(fmt.Sprintf("    -> %s (references removed symbol(s))\n", af))
			shown++
		}
	}
	if totalAffected > maxImpactFiles {
		b.WriteString(fmt.Sprintf("  ... and %d more\n", totalAffected-maxImpactFiles))
	}
	b.WriteString("\nVerify with `go build` after fixing these files.")

	debug.Log("cross_impact", "detected %d affected files across %d edited files", totalAffected, len(impacts))
	return b.String()
}

// extractImpactRemovedSymbols parses old and new Go source, returns symbols
// present in old but absent from new.
func extractImpactRemovedSymbols(oldContent, newContent, filename string) []impactRemovedSymbol {
	oldSyms := extractImpactSymbols(oldContent, filename)
	newSyms := extractImpactSymbols(newContent, filename)

	if len(oldSyms) == 0 {
		return nil
	}

	newSet := make(map[string]bool, len(newSyms))
	for _, s := range newSyms {
		newSet[s.name+s.category] = true
	}

	var removed []impactRemovedSymbol
	for _, s := range oldSyms {
		if !newSet[s.name+s.category] {
			removed = append(removed, s)
		}
	}
	return removed
}

// extractImpactSymbols parses Go source and returns all exported top-level
// declarations (functions, types, variables, constants) and exported methods.
func extractImpactSymbols(src, filename string) []impactRemovedSymbol {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil
	}

	var syms []impactRemovedSymbol

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil || !ast.IsExported(d.Name.Name) {
				continue
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvType := impactReceiverTypeName(d.Recv.List[0].Type)
				if recvType != "" {
					syms = append(syms, impactRemovedSymbol{
						name:     fmt.Sprintf("(*%s).%s", recvType, d.Name.Name),
						category: "method",
					})
				}
			} else {
				syms = append(syms, impactRemovedSymbol{
					name:     d.Name.Name,
					category: "func",
				})
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name != nil && ast.IsExported(s.Name.Name) {
						syms = append(syms, impactRemovedSymbol{
							name:     s.Name.Name,
							category: "type",
						})
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if ast.IsExported(name.Name) {
							cat := "var"
							if d.Tok == token.CONST {
								cat = "const"
							}
							syms = append(syms, impactRemovedSymbol{
								name:     name.Name,
								category: cat,
							})
						}
					}
				}
			}
		}
	}

	return syms
}

// impactReceiverTypeName extracts the type name from a receiver expression.
func impactReceiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// findSiblingGoFiles returns Go files in the same directory as the edited
// file, excluding the edited file itself and test files (unless the edited
// file is a test file).
func findSiblingGoFiles(dir, editedFile string, max int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	isTestFile := strings.HasSuffix(editedFile, "_test.go")

	var siblings []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".go" {
			continue
		}
		fullPath := filepath.Join(dir, name)
		if fullPath == editedFile {
			continue
		}
		siblingIsTest := strings.HasSuffix(name, "_test.go")
		if siblingIsTest && !isTestFile {
			continue
		}
		siblings = append(siblings, fullPath)
		if len(siblings) >= max {
			break
		}
	}
	return siblings
}

// referencesAnyImpactSymbol checks whether the given Go source references any
// of the removed symbols.
func referencesAnyImpactSymbol(src string, removed []impactRemovedSymbol) bool {
	for _, s := range removed {
		var name string
		switch s.category {
		case "method":
			parts := strings.Split(s.name, ").")
			if len(parts) == 2 {
				name = parts[1]
			}
		default:
			name = s.name
		}
		if name == "" {
			continue
		}
		if containsGoIdent(src, name) {
			return true
		}
	}
	return false
}

// containsGoIdent checks if name appears as a Go identifier in src.
func containsGoIdent(src, name string) bool {
	idx := 0
	for {
		pos := strings.Index(src[idx:], name)
		if pos < 0 {
			return false
		}
		pos += idx
		end := pos + len(name)

		if pos > 0 {
			prev := src[pos-1]
			if isIdentChar(prev) {
				idx = end
				continue
			}
		}
		if end < len(src) {
			next := src[end]
			if isIdentChar(next) {
				idx = end
				continue
			}
		}
		return true
	}
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '_'
}

// gitFileContentAtHEAD returns the file content from the last committed version.
func gitFileContentAtHEAD(workingDir, relPath string) (string, error) {
	return runGitShowWithTimeout(workingDir, relPath, gitDiffTimeout)
}

// runGitShowWithTimeout runs `git show HEAD:path` with a timeout.
func runGitShowWithTimeout(workingDir, relPath string, timeout time.Duration) (string, error) {
	cmd := exec.Command("git", "-C", workingDir, "show", "HEAD:"+relPath)
	output, err := runGitCommandWithTimeout(cmd, timeout)
	if err != nil {
		return "", err
	}
	return output, nil
}
