package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	docDriftMinGoFiles = 3
)

type docDriftState struct {
	fired bool
}

func newDocDriftState() *docDriftState { return &docDriftState{} }

func (a *Agent) checkDocDriftGate(runStats *RunStats) string {
	if a.docDrift == nil || a.docDrift.fired {
		return ""
	}
	a.docDrift.fired = true
	return docDriftAdvisory(runStats.FilesEdited)
}

func (s *docDriftState) reset() { s.fired = false }

func isDocFile(slashPath string) bool {
	return strings.HasSuffix(slashPath, ".md") ||
		strings.HasSuffix(slashPath, ".mdx") ||
		strings.HasSuffix(slashPath, ".rst")
}

func normalizePathSlashes(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

func goPackageDir(slashPath string) string {
	idx := strings.LastIndex(slashPath, "/")
	if idx <= 0 {
		return ""
	}
	return slashPath[:idx]
}

func docDriftAdvisory(filesEdited []string) string {
	if len(filesEdited) == 0 {
		return ""
	}
	goFiles := 0
	docsTouched := false
	pkgs := map[string]bool{}
	for _, f := range filesEdited {
		rel := normalizePathSlashes(filepath.ToSlash(f))

		if strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") {
			goFiles++
			if p := goPackageDir(rel); p != "" {
				pkgs[p] = true
			}
		} else if isDocFile(rel) {
			docsTouched = true
		}
	}
	if docsTouched || goFiles < docDriftMinGoFiles {
		return ""
	}
	debug.Log("doc-drift", "advisory: %d Go files edited across %d package(s), no docs touched", goFiles, len(pkgs))
	return fmt.Sprintf(
		"[doc-drift] This run edited %d Go files across %d package(s) but touched no documentation (README, docs/*.md). "+
			"If any change affects user-visible behavior (commands, flags, config, APIs), run the `documentation-update` skill before finishing. "+
			"For multi-package changes, update docs leaf-to-root: run `dep_graph` (action=reverse_deps) on the changed packages so modules that depend on them get doc updates first, repo-level docs (README, docs/guide) last.",
		goFiles, len(pkgs))
}
