package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Documentation Drift Detector
//
// Research basis:
//   - "Remember Your Trace: Memory-Guided Long-Horizon Agentic Framework
//     for Consistent and Hierarchical Repository-Level Code Documentation"
//     (MemDocAgent, NeurIPS 2026): repository documentation stays consistent
//     only when updates follow a dependency-constrained order (leaf modules
//     before the packages that depend on them, repo-level docs last). Single
//     diff-driven passes miss this ordering and let module READMEs,
//     component docs, and the repository README drift apart.
//
// Problem: AI coding agents routinely finish multi-file code runs without
// touching any documentation. The bundled documentation-update skill exists
// but is only invoked when the agent remembers it -- there is no run-level
// signal that a code-heavy run left every doc untouched. Over many runs the
// version numbers, command flags, and architecture descriptions in README /
// docs/ silently rot relative to the code.
//
// What it detects: at run end, when >= docDriftMinGoFiles Go source files
// were edited but not a single documentation file (.md/.mdx/.rst) was
// touched, it emits one advisory pointing at the documentation-update skill
// and the leaf-to-root update order (dep_graph reverse_deps).
//
// Distinct from existing detectors:
//   - doc_staleness (todo_staleness family): scans TODO markers inside docs;
//     this detector is about the ABSENCE of doc edits after code edits.
//   - commit_hint: reminds about uncommitted changes; this detector reminds
//     about un-documented changes.
//   - edit_propagation.go: tracks code->code cascades; this tracks the
//     missing code->docs cascade.

const (
	// docDriftMinGoFiles is the minimum number of edited Go files that makes
	// a run "code-heavy" enough that untouched docs are worth mentioning.
	// Below this, doc updates are genuinely often unnecessary (small fixes).
	docDriftMinGoFiles = 3
)

// docDriftState tracks whether the advisory already fired this run.
type docDriftState struct {
	fired bool
}

func newDocDriftState() *docDriftState { return &docDriftState{} }

// checkDocDriftGate is the run-end advisory hook (wired after the commit
// hint gate in agent.go). One shot per run.
func (a *Agent) checkDocDriftGate(runStats *RunStats) string {
	if a.docDrift == nil || a.docDrift.fired {
		return ""
	}
	a.docDrift.fired = true
	return docDriftAdvisory(runStats.FilesEdited)
}

// reset clears per-run state.
func (s *docDriftState) reset() { s.fired = false }

// isDocFile reports whether a path is a documentation file.
func isDocFile(slashPath string) bool {
	return strings.HasSuffix(slashPath, ".md") ||
		strings.HasSuffix(slashPath, ".mdx") ||
		strings.HasSuffix(slashPath, ".rst")
}

// normalizePathSlashes returns p with Windows backslash separators
// converted to forward slashes. FilesEdited carries the literal tool
// argument, which on Windows is backslash-separated; parsing it on any
// host must treat those as separators for package counting.
func normalizePathSlashes(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// goPackageDir extracts the package directory from a normalized slashed Go
// file path, e.g. "internal/tool/browser.go" -> "internal/tool". Returns ""
// when the file sits at the repository root. Uses string slicing (not
// filepath.Dir) so Windows-style inputs parse identically on every host.
func goPackageDir(slashPath string) string {
	idx := strings.LastIndex(slashPath, "/")
	if idx <= 0 {
		return ""
	}
	return slashPath[:idx]
}

// docDriftAdvisory inspects the run's edited files and returns an advisory
// when the run was code-heavy with zero documentation touches. Pure function
// over the file list.
func docDriftAdvisory(filesEdited []string) string {
	if len(filesEdited) == 0 {
		return ""
	}
	goFiles := 0
	docsTouched := false
	pkgs := map[string]bool{}
	for _, f := range filesEdited {
		rel := normalizePathSlashes(filepath.ToSlash(f))
		// Absolute workspace paths are fine: only suffix/dir logic is used.
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
