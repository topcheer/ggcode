package agent

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

const maxImpactFiles = 8

const maxScanFiles = 30

const impactScanTimeout = 5 * time.Second

type impactRemovedSymbol struct {
	name     string
	category string
}

type fileImpact struct {
	editedFile    string
	removedSyms   []impactRemovedSymbol
	affectedFiles []string
}

func (a *Agent) checkCrossFileImpact(runStats *RunStats) string {
	if a.crossFileImpact == nil {
		return ""
	}
	a.crossFileImpact.mu.Lock()
	if a.crossFileImpact.fired {
		a.crossFileImpact.mu.Unlock()
		return ""
	}
	a.crossFileImpact.mu.Unlock()

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}

	goFiles := editedGoFiles(runStats.FilesEdited)
	if len(goFiles) == 0 {
		return ""
	}

	editedAbs := absEditedSet(workingDir, goFiles)

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
		if imp := analyzeEditedFileImpact(workingDir, editedFile, editedAbs, deadline); imp != nil {
			impacts = append(impacts, *imp)
		}
	}

	if len(impacts) == 0 {
		return ""
	}

	totalAffected, totalRemoved := impactTotals(impacts)

	a.crossFileImpact.mu.Lock()
	if a.crossFileImpact.fired {
		a.crossFileImpact.mu.Unlock()
		return ""
	}
	a.crossFileImpact.fired = true
	a.crossFileImpact.mu.Unlock()

	warning := renderImpactWarning(impacts, totalRemoved, totalAffected)

	debug.Log("cross_impact", "detected %d affected files across %d edited files", totalAffected, len(impacts))
	return warning
}

func editedGoFiles(files []string) []string {
	var goFiles []string
	for _, f := range files {
		if filepath.Ext(f) == ".go" {
			goFiles = append(goFiles, f)
		}
	}
	return goFiles
}

func absEditedSet(workingDir string, goFiles []string) map[string]bool {
	editedAbs := make(map[string]bool, len(goFiles))
	for _, f := range goFiles {
		af := f
		if !filepath.IsAbs(af) {
			af = filepath.Join(workingDir, af)
		}
		editedAbs[filepath.Clean(af)] = true
	}
	return editedAbs
}

func impactTotals(impacts []fileImpact) (affected, removed int) {
	for _, imp := range impacts {
		affected += len(imp.affectedFiles)
		removed += len(imp.removedSyms)
	}
	return affected, removed
}

type impactPending struct {
	relPath string
	file    *ast.File
	near    []impactNearMiss
}

type impactSiblingScan struct {
	affectedSet map[string]bool
	pkgFiles    []*ast.File
	pendings    []impactPending
}

func analyzeEditedFileImpact(workingDir, editedFile string, editedAbs map[string]bool, deadline time.Time) *fileImpact {
	absPath := editedFile
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(workingDir, editedFile)
	}

	newContent, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}

	oldContent, err := gitFileContentAtHEAD(workingDir, editedFile)
	if err != nil || oldContent == "" {
		return nil
	}

	removed := extractImpactRemovedSymbols(oldContent, string(newContent), absPath)
	if len(removed) == 0 {
		return nil
	}

	dir := filepath.Dir(absPath)
	siblings := findSiblingGoFiles(dir, absPath, maxScanFiles)
	if len(siblings) == 0 {
		return nil
	}

	names, methodOwners := impactNameTables(removed)
	if len(names) == 0 {
		return nil
	}

	dirFset := token.NewFileSet()
	scan := scanSiblingsForImpact(dirFset, siblings, editedAbs, workingDir, names, methodOwners, deadline)
	resolveImpactPendings(dirFset, &scan, absPath, string(newContent), methodOwners, deadline)

	if len(scan.affectedSet) == 0 {
		return nil
	}
	affected := make([]string, 0, len(scan.affectedSet))
	for f := range scan.affectedSet {
		affected = append(affected, f)
	}
	return &fileImpact{
		editedFile:    editedFile,
		removedSyms:   removed,
		affectedFiles: affected,
	}
}

func scanSiblingsForImpact(dirFset *token.FileSet, siblings []string, editedAbs map[string]bool, workingDir string, names map[string]bool, methodOwners map[string]map[string]bool, deadline time.Time) impactSiblingScan {
	scan := impactSiblingScan{affectedSet: make(map[string]bool)}
	for _, sibling := range siblings {
		if time.Now().After(deadline) {
			break
		}
		if editedAbs[filepath.Clean(sibling)] {
			continue
		}
		content, err := os.ReadFile(sibling)
		if err != nil {
			continue
		}
		relPath, _ := filepath.Rel(workingDir, sibling)
		if relPath == "" {
			relPath = sibling
		}
		file, perr := parser.ParseFile(dirFset, sibling, content, 0)
		if perr != nil {

			for name := range names {
				if containsGoIdent(string(content), name) {
					scan.affectedSet[relPath] = true
					break
				}
			}
			continue
		}
		scan.pkgFiles = append(scan.pkgFiles, file)
		found, near := walkImpactRefs(file, names, methodOwners)
		if found {
			scan.affectedSet[relPath] = true
			continue
		}
		if len(near) > 0 {
			scan.pendings = append(scan.pendings, impactPending{relPath: relPath, file: file, near: near})
		}
	}
	return scan
}

func resolveImpactPendings(dirFset *token.FileSet, scan *impactSiblingScan, editedAbsPath, newContent string, methodOwners map[string]map[string]bool, deadline time.Time) {
	if len(scan.pendings) == 0 || len(methodOwners) == 0 || !time.Now().Before(deadline) {
		return
	}
	if ef, perr := parser.ParseFile(dirFset, editedAbsPath, newContent, 0); perr == nil {
		scan.pkgFiles = append(scan.pkgFiles, ef)
	}
	infos := impactTypeCheckGroups(dirFset, scan.pkgFiles)
	for _, p := range scan.pendings {
		info := infos[p.file.Name.Name]
		for _, m := range p.near {
			if impactResolveNearMiss(info, m, methodOwners) {
				scan.affectedSet[p.relPath] = true
				break
			}
		}
	}
}

func renderImpactWarning(impacts []fileImpact, totalRemoved, totalAffected int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"[cross-file impact analysis] Your edits removed or renamed %d internal (same-package) symbol(s) "+
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
	return b.String()
}

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

func extractImpactSymbols(src, filename string) []impactRemovedSymbol {
	file, _, err := parseGoSource(filename, src, 0)
	if err != nil {
		return nil
	}

	var syms []impactRemovedSymbol

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:

			if d.Name == nil || ast.IsExported(d.Name.Name) {
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
					if s.Name != nil && !ast.IsExported(s.Name.Name) {
						syms = append(syms, impactRemovedSymbol{
							name:     s.Name.Name,
							category: "type",
						})
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if !ast.IsExported(name.Name) {
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

func impactNameTables(removed []impactRemovedSymbol) (map[string]bool, map[string]map[string]bool) {
	names := make(map[string]bool, len(removed))
	methodOwners := make(map[string]map[string]bool)
	for _, s := range removed {
		var name string
		switch s.category {
		case "method":
			parts := strings.Split(s.name, ").")
			if len(parts) == 2 {
				name = parts[1]
			} else if idx := strings.LastIndex(s.name, "."); idx > 0 {
				name = s.name[idx+1:]
			}
			if name != "" {
				idx := strings.LastIndex(s.name, ".")
				owner := strings.Trim(s.name[:idx], "(* )")
				if owner != "" {
					if methodOwners[name] == nil {
						methodOwners[name] = make(map[string]bool)
					}
					methodOwners[name][owner] = true
				}
			}
		default:
			name = s.name
		}
		if name != "" {
			names[name] = true
		}
	}
	return names, methodOwners
}

func referencesAnyImpactSymbol(src string, removed []impactRemovedSymbol) bool {
	names, methodOwners := impactNameTables(removed)
	if len(names) == 0 {
		return false
	}

	file, _, err := parseGoSource("sibling.go", src, 0)
	if err != nil {
		for name := range names {
			if containsGoIdent(src, name) {
				return true
			}
		}
		return false
	}
	found, _ := walkImpactRefs(file, names, methodOwners)
	return found
}

func walkImpactRefs(file *ast.File, names map[string]bool, methodOwners map[string]map[string]bool) (bool, []impactNearMiss) {
	v := &impactRefVisitor{names: names, methodOwners: methodOwners}
	ast.Walk(v, file)
	return v.found, v.nearMiss
}

const maxImpactNearMiss = 32

type impactNearMiss struct {
	recv ast.Expr
	sel  string
}

type impactRefVisitor struct {
	names        map[string]bool
	methodOwners map[string]map[string]bool
	nearMiss     []impactNearMiss
	found        bool
}

func (v *impactRefVisitor) Visit(n ast.Node) ast.Visitor {
	if v.found || n == nil {
		return nil
	}
	switch node := n.(type) {
	case *ast.SelectorExpr:

		if owners := v.methodOwners[node.Sel.Name]; len(owners) > 0 {
			if base := selectorBaseTypeName(node.X); base != "" && owners[base] {
				v.found = true
				return nil
			}

			if len(v.nearMiss) < maxImpactNearMiss {
				v.nearMiss = append(v.nearMiss, impactNearMiss{recv: node.X, sel: node.Sel.Name})
			}
		}

		ast.Walk(v, node.X)
		return nil
	case *ast.CompositeLit:
		for _, elt := range node.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {

				ast.Walk(v, kv.Value)
				continue
			}
			ast.Walk(v, elt)
		}
		return nil
	case *ast.Ident:

		if v.names[node.Name] && len(v.methodOwners[node.Name]) == 0 && node.Obj == nil {
			v.found = true
			return nil
		}
	}
	return v
}

func selectorBaseTypeName(x ast.Expr) string {
	for {
		switch e := x.(type) {
		case *ast.ParenExpr:
			x = e.X
		case *ast.StarExpr:
			x = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}

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

func gitFileContentAtHEAD(workingDir, relPath string) (string, error) {

	repoRoot := gitRepoTopLevel(workingDir)

	absWork := workingDir
	if !filepath.IsAbs(absWork) {
		if a, err := filepath.Abs(absWork); err == nil {
			absWork = a
		}
	}

	symWork, symRoot := absWork, repoRoot
	if r, err := filepath.EvalSymlinks(absWork); err == nil {
		symWork = r
	}
	if repoRoot != "" {
		if r, err := filepath.EvalSymlinks(repoRoot); err == nil {
			symRoot = r
		}
	}
	target := relPath
	if filepath.IsAbs(target) {
		base := symRoot
		if base == "" {
			base = symWork
		}

		if r, err := filepath.EvalSymlinks(target); err == nil {
			target = r
		}
		rel, err := filepath.Rel(base, target)
		if err != nil {
			return "", err
		}
		target = rel
	} else if symRoot != "" && symWork != symRoot && strings.HasPrefix(symWork, symRoot+string(filepath.Separator)) {

		target = filepath.Join(strings.TrimPrefix(symWork, symRoot+string(filepath.Separator)), target)
	}
	target = filepath.Clean(target)
	if target == ".." || strings.HasPrefix(target, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes repo root", target)
	}
	return runGitShowWithTimeout(workingDir, target, gitDiffTimeout)
}

func gitRepoTopLevel(workingDir string) string {
	cmd := exec.Command("git", "-C", workingDir, "rev-parse", "--show-toplevel")
	out, err := runGitCommandWithTimeout(cmd, gitDiffTimeout)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func runGitShowWithTimeout(workingDir, relPath string, timeout time.Duration) (string, error) {
	cmd := exec.Command("git", "-C", workingDir, "show", "HEAD:"+relPath)
	output, err := runGitCommandWithTimeout(cmd, timeout)
	if err != nil {
		return "", err
	}
	return output, nil
}
