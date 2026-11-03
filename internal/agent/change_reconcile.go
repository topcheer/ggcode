package agent

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

type changeReconcileState struct {
	fired          bool
	preRunDirty    map[string]bool
	preRunCaptured bool
}

func newChangeReconcileState() *changeReconcileState {
	return &changeReconcileState{
		preRunDirty: make(map[string]bool),
	}
}

func (c *changeReconcileState) capturePreRunState(workingDir string) {
	if c.preRunCaptured || workingDir == "" {
		return
	}
	c.preRunCaptured = true

	files, err := gitChangedFiles(workingDir)
	if err != nil {
		debug.Log("reconcile", "capturePreRunState: git diff failed: %v", err)
		return
	}
	for _, f := range files {
		c.preRunDirty[normalizeReconcilePath(workingDir, f)] = true
	}
	if len(files) > 0 {
		debug.Log("reconcile", "capturePreRunState: %d files were dirty before run start", len(files))
	}
}

func (c *changeReconcileState) dirtyFileCount() int {
	return len(c.preRunDirty)
}

func (c *changeReconcileState) reset() {
	c.fired = false
	c.preRunCaptured = false
	c.preRunDirty = make(map[string]bool)
}

var sideEffectFiles = map[string]bool{
	"go.sum":            true,
	"go.mod":            true,
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
	"composer.lock":     true,
	"Gemfile.lock":      true,
	"Cargo.lock":        true,
	"poetry.lock":       true,
	"uv.lock":           true,
	".DS_Store":         true,
	"Thumbs.db":         true,
	".python-version":   true,
	".node-version":     true,
	".tool-versions":    true,
}

var sourceCodeExts = map[string]bool{
	".go": true, ".rs": true, ".py": true, ".js": true, ".ts": true,
	".jsx": true, ".tsx": true, ".java": true, ".kt": true, ".rb": true,
	".php": true, ".c": true, ".cpp": true, ".h": true, ".hpp": true,
	".cs": true, ".swift": true, ".dart": true, ".scala": true,
	".sh": true, ".bash": true, ".zsh": true,
	".vue": true, ".svelte": true,
	".sql": true, ".proto": true, ".graphql": true, ".gql": true,
	".yaml": true, ".yml": true, ".toml": true, ".json": true,
	".html": true, ".css": true, ".scss": true, ".less": true,
	".md": true,
}

const gitDiffTimeout = 5 * time.Second

const maxUnexpectedFiles = 10

func (a *Agent) checkChangeReconcile(runStats *RunStats) string {
	if a.changeReconcile == nil {
		return ""
	}
	if a.changeReconcile.fired {
		return ""
	}
	a.changeReconcile.fired = true

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}

	gitChanged, err := gitChangedFiles(workingDir)
	if err != nil {
		debug.Log("reconcile", "git diff failed: %v", err)
		return ""
	}
	if len(gitChanged) == 0 {
		return ""
	}

	edited := make(map[string]bool, len(runStats.FilesEdited))
	for _, f := range runStats.FilesEdited {
		edited[normalizeReconcilePath(workingDir, f)] = true
	}

	var unexpected []string
	for _, f := range gitChanged {
		base := filepath.Base(f)
		if sideEffectFiles[base] {
			continue
		}
		ext := filepath.Ext(f)
		if !sourceCodeExts[ext] {
			continue
		}
		normalized := normalizeReconcilePath(workingDir, f)
		if edited[normalized] {
			continue
		}

		if a.changeReconcile.preRunDirty[normalized] {
			continue
		}
		unexpected = append(unexpected, f)
	}

	if len(unexpected) == 0 {
		return ""
	}

	sort.Strings(unexpected)

	display := unexpected
	if len(display) > maxUnexpectedFiles {
		display = display[:maxUnexpectedFiles]
	}

	msg := fmt.Sprintf(
		"[change reconciliation] %d source file(s) changed in git but were NOT "+
			"explicitly edited by you. These are likely side effects from shell commands "+
			"(e.g., code generation, format-on-save, or build tools). Review these "+
			"unexpected changes before finishing:\n",
		len(unexpected),
	)
	for _, f := range display {
		msg += fmt.Sprintf("  - %s\n", f)
	}
	if len(unexpected) > maxUnexpectedFiles {
		msg += fmt.Sprintf("  ... and %d more\n", len(unexpected)-maxUnexpectedFiles)
	}
	msg += "\nIf these changes are intentional (e.g., generated code), you can ignore this warning. "

	debug.Log("reconcile", "detected %d unexpected changed files", len(unexpected))
	return msg
}

func gitChangedFiles(workingDir string) ([]string, error) {
	cmd := exec.Command("git", "-C", workingDir, "diff", "--name-only", "HEAD")
	output, err := runGitCommandWithTimeout(cmd, gitDiffTimeout)
	if err != nil {

		cmd2 := exec.Command("git", "-C", workingDir, "diff", "--name-only")
		output, err = runGitCommandWithTimeout(cmd2, gitDiffTimeout)
		if err != nil {
			return nil, err
		}
	}

	output = strings.TrimSpace(output)
	var files []string

	lines := strings.Split(output, "\n")
	files = make([]string, 0, len(lines))
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}

	if st, err := runGitCommandWithTimeout(
		gitCommand(workingDir, "status", "--porcelain", "-uall"),
		gitDiffTimeout,
	); err == nil {
		seen := make(map[string]bool, len(files))
		for _, f := range files {
			seen[f] = true
		}
		for _, line := range strings.Split(st, "\n") {
			if !strings.HasPrefix(line, "??") {
				continue
			}
			f := strings.TrimSpace(line[2:])
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			files = append(files, f)
		}
	}
	return files, nil
}

func runGitCommandWithTimeout(cmd *exec.Cmd, timeout time.Duration) (string, error) {
	timer := time.AfterFunc(timeout, func() {
		_ = cmd.Process.Kill()
	})
	defer timer.Stop()

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func normalizeReconcilePath(workingDir, path string) string {
	wd, err := filepath.Abs(workingDir)
	if err != nil {
		wd = filepath.Clean(workingDir)
	}
	p := filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(p) {

		p = filepath.Join(wd, p)
	}

	rel, rerr := filepath.Rel(wd, p)
	if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return rel
}
