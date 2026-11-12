package agent

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// turnImpactRecorder aggregates the blast radius of one run: files touched
// by edit-class tools and shell mutations (with a conservative destructive
// flag). The aggregated report is injected as a read-only observation at the
// START of the next run (design constraint from arXiv:2611.02815: predictive
// reports must inform, never block).
type turnImpactRecorder struct {
	mu     sync.Mutex
	edits  map[string]string // path -> first tool that touched it
	shells []turnShellRecord
}

type turnShellRecord struct {
	Cmd         string
	Destructive bool
}

func newTurnImpactRecorder() *turnImpactRecorder {
	return &turnImpactRecorder{edits: map[string]string{}}
}

func (t *turnImpactRecorder) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.edits = map[string]string{}
	t.shells = nil
}

func (t *turnImpactRecorder) recordFileEdit(tool, path string) {
	if path == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.edits == nil {
		t.edits = map[string]string{}
	}
	if _, ok := t.edits[path]; !ok {
		t.edits[path] = tool
	}
}

func (t *turnImpactRecorder) recordShellMutation(cmd string, destructive bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.shells = append(t.shells, turnShellRecord{Cmd: cmd, Destructive: destructive})
}

// turnImpactReport is the aggregated view; empty string means "nothing
// write-capable happened" so callers can skip injection entirely.
type turnImpactReport struct {
	Files       []string // sorted, deduped
	FirstTools  map[string]string
	ShellCount  int
	Destructive int
	RiskTier    string // low | medium | high
}

func (t *turnImpactRecorder) report() *turnImpactReport {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.edits) == 0 && len(t.shells) == 0 {
		return nil
	}
	files := make([]string, 0, len(t.edits))
	for p := range t.edits {
		files = append(files, p)
	}
	sort.Strings(files)
	destructive := 0
	for _, s := range t.shells {
		if s.Destructive {
			destructive++
		}
	}
	tier := "low"
	switch {
	case destructive > 0:
		tier = "high"
	case len(t.shells) > 0 || len(files) > 5:
		tier = "medium"
	}
	return &turnImpactReport{
		Files:       files,
		FirstTools:  t.edits,
		ShellCount:  len(t.shells),
		Destructive: destructive,
		RiskTier:    tier,
	}
}

// render turns the report into the injected observation text.
func (r *turnImpactReport) render() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Turn Impact (previous run, informational only)\n")
	fmt.Fprintf(&b, "Risk tier: %s\n", r.RiskTier)
	if len(r.Files) > 0 {
		// Route worktree-resident paths to their worktree so changes made in
		// an isolated .ggcode/worktrees/<name> checkout are never attributed
		// to the main tree (#5524). Main-tree files keep the original format
		// byte-for-byte; a worktree-only run produces zero main-tree lines.
		var mainFiles []string
		worktreeFiles := map[string][]string{}
		for _, p := range r.Files {
			if name, _, ok := worktreeSegment(p); ok {
				worktreeFiles[name] = append(worktreeFiles[name], p)
				continue
			}
			mainFiles = append(mainFiles, p)
		}
		if len(mainFiles) > 0 {
			fmt.Fprintf(&b, "Files touched (%d):\n", len(r.Files))
			for _, p := range mainFiles {
				fmt.Fprintf(&b, "- %s (via %s)\n", p, r.FirstTools[p])
			}
		}
		wtNames := make([]string, 0, len(worktreeFiles))
		for name := range worktreeFiles {
			wtNames = append(wtNames, name)
		}
		sort.Strings(wtNames)
		for _, name := range wtNames {
			fmt.Fprintf(&b, "Files touched in worktree %s (isolated checkout under .ggcode/worktrees - NOT the main tree):\n", name)
			for _, p := range worktreeFiles[name] {
				fmt.Fprintf(&b, "- %s (via %s)\n", p, r.FirstTools[p])
			}
		}
	}
	if r.ShellCount > 0 {
		fmt.Fprintf(&b, "Source-mutating shell commands: %d", r.ShellCount)
		if r.Destructive > 0 {
			fmt.Fprintf(&b, " (destructive: %d)", r.Destructive)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// resolveImpactPath anchors relative edit paths against the session working
// directory so files changed inside a worktree keep their worktree
// provenance (#5524). Absolute paths are cleaned only.
func resolveImpactPath(workingDir, p string) string {
	if p == "" {
		return p
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if workingDir == "" {
		return filepath.Clean(p)
	}
	return filepath.Join(workingDir, p)
}

// worktreeSegment reports whether a changed path lives inside a ggcode-
// managed worktree (<repo>/.ggcode/worktrees/<name>/...). It returns the
// worktree name and the path relative to the repo root; ok is false for
// main-tree paths.
func worktreeSegment(p string) (name, repoRel string, ok bool) {
	const marker = ".ggcode/worktrees/"
	norm := filepath.ToSlash(p)
	i := strings.Index(norm, marker)
	if i < 0 {
		return "", "", false
	}
	rest := norm[i+len(marker):]
	j := strings.Index(rest, "/")
	if j <= 0 {
		return "", "", false
	}
	name = rest[:j]
	repoRel = norm[:i] + ".ggcode/worktrees/" + rest
	if name == "" || repoRel == "" {
		return "", "", false
	}
	return name, repoRel, true
}

// shellDestructiveRe is a conservative keyword check: over-reporting is
// acceptable (report-only), under-reporting is not.
func shellDestructive(cmd string) bool {
	c := strings.ToLower(cmd)
	for _, k := range []string{
		"git push", "git reset", "git clean", "git checkout --", "git restore",
		"git rebase", "git filter-branch", "rm -rf", "mkfs", "dd if=", "> /dev/sd",
		"shutdown", "reboot",
	} {
		if strings.Contains(c, k) {
			return true
		}
	}
	return false
}
