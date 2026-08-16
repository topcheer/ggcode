package agent

// Pre-Completion Change Reconciliation Gate
//
// Research basis: A 2026 industry analysis of AI coding agent failure modes
// identifies "unintended side-effect changes" as a top-5 quality issue. When
// agents run shell commands (go mod tidy, npm install, code generation tools,
// format-on-save hooks), these commands can modify source files the agent
// didn't explicitly edit. The agent declares "done" without knowing about
// these collateral changes, leading to:
//
//   - Unexpected files in commits
//   - Auto-generated code that hasn't been reviewed
//   - Configuration drift from tooling commands
//   - Lock file changes that mask real dependency issues
//
// Competitor analysis:
//   - Claude Code: shows full diff before commit, but no automatic reconciliation
//   - Cursor: diff review step in IDE, requires user action
//   - Devin: post-completion summary includes all changed files
//   - OpenHands/Cline: no reconciliation; relies on git diff review
//   - Aider: commits per-edit, so side effects are visible between steps
//
// ggcode's approach: after all other pre-completion gates pass (todos,
// fulfillment, companion, verify, complexity), run `git diff --name-only`
// and compare against runStats.FilesEdited. Flag source files that changed
// but were NOT explicitly edited by the agent — these are side effects.
// Common side-effect files (lock files, generated files) are excluded to
// minimize false positives.
//
// This is zero-LLM-cost, runs in <50ms (git diff --name-only is very fast),
// and fires at most once per run.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// changeReconcileState tracks whether the reconciliation gate has fired
// and stores the set of files that were already dirty before the run started.
type changeReconcileState struct {
	fired          bool
	preRunDirty    map[string]bool // normalized paths of files changed before this run
	preRunCaptured bool            // true after capturePreRunState has executed
}

func newChangeReconcileState() *changeReconcileState {
	return &changeReconcileState{
		preRunDirty: make(map[string]bool),
	}
}

// capturePreRunState records the set of files with uncommitted changes
// relative to HEAD at the START of a run. This lets checkChangeReconcile
// distinguish pre-existing dirty files (user's own work) from genuine
// side-effect changes introduced by the agent's tool calls.
//
// Without this, any file the user left uncommitted before invoking the
// agent would be flagged as an "unexpected side effect" — a false positive
// that undermines trust in the reconciliation gate.
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

// dirtyFileCount returns the number of pre-existing dirty files (for
// run-start awareness injection).
func (c *changeReconcileState) dirtyFileCount() int {
	return len(c.preRunDirty)
}

// reset clears the state so the gate can fire on a new run.
// Called once at the start of each RunStreamWithContent.
func (c *changeReconcileState) reset() {
	c.fired = false
	c.preRunCaptured = false
	c.preRunDirty = make(map[string]bool)
}

// sideEffectFiles are files commonly modified by tooling commands (go mod tidy,
// npm install, code generators, etc.). Changes to these files are expected
// side effects and should NOT trigger a warning.
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

// sourceCodeExts are extensions that indicate a source code file. Only
// unexpected changes to source files trigger the warning — binary files,
// config files, and lock files are ignored.
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

// gitDiffTimeout bounds the git diff command to prevent hangs in large repos
// or network-mounted filesystems.
const gitDiffTimeout = 5 * time.Second

// maxUnexpectedFiles caps the number of unexpected files listed in the warning
// to avoid flooding the agent context when a code generator modifies many files.
const maxUnexpectedFiles = 10

// checkChangeReconcile runs `git diff --name-only HEAD` and compares the
// changed files against runStats.FilesEdited. Returns a non-empty warning
// string if unexpected source file changes are detected.
//
// The gate fires at most once per run. It is advisory — it doesn't block
// completion but alerts the agent to review unintended changes before finishing.
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

	// Get files changed according to git.
	gitChanged, err := gitChangedFiles(workingDir)
	if err != nil {
		debug.Log("reconcile", "git diff failed: %v", err)
		return ""
	}
	if len(gitChanged) == 0 {
		return ""
	}

	// Build a set of files the agent explicitly edited (normalized).
	edited := make(map[string]bool, len(runStats.FilesEdited))
	for _, f := range runStats.FilesEdited {
		edited[normalizeReconcilePath(workingDir, f)] = true
	}

	// Find unexpected changes: in git diff, NOT explicitly edited, and is a
	// source code file (not a known side-effect file).
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
		// Skip files that were already dirty before this run started.
		// These are the user's own uncommitted changes, not agent side effects.
		if a.changeReconcile.preRunDirty[normalized] {
			continue
		}
		unexpected = append(unexpected, f)
	}

	if len(unexpected) == 0 {
		return ""
	}

	// #550 D2: deterministic ordering — git diff order varies with index
	// state, which made the truncated display list flap between otherwise
	// identical runs.
	sort.Strings(unexpected)

	// Build the warning message.
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

// gitChangedFiles returns the list of files with uncommitted changes relative
// to HEAD. Returns the raw output of `git diff --name-only HEAD`.
func gitChangedFiles(workingDir string) ([]string, error) {
	cmd := exec.Command("git", "-C", workingDir, "diff", "--name-only", "HEAD")
	output, err := runGitCommandWithTimeout(cmd, gitDiffTimeout)
	if err != nil {
		// If HEAD doesn't exist (new repo with no commits), try unstaged diff.
		cmd2 := exec.Command("git", "-C", workingDir, "diff", "--name-only")
		output, err = runGitCommandWithTimeout(cmd2, gitDiffTimeout)
		if err != nil {
			return nil, err
		}
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return nil, nil
	}

	// #550 D2: trim each line and drop empties — git output carrying CR
	// line endings (core.autocrlf checkouts) or stray padding previously
	// produced keys like "foo.go\r" that never matched the edited set,
	// flagging every changed file as unreconciled.
	lines := strings.Split(output, "\n")
	files := make([]string, 0, len(lines))
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// runGitCommandWithTimeout runs a git command with a timeout. Returns trimmed
// stdout output.
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

// normalizeReconcilePath converts a file path to a canonical form for
// comparison. Both sides — git diff output (repo-relative) and
// FilesEdited (frequently absolute, as edit_file records what the agent
// passed) — reduce to a repo-root-relative path so the SAME file compares
// equal regardless of which form it arrived in.
func normalizeReconcilePath(workingDir, path string) string {
	wd, err := filepath.Abs(workingDir)
	if err != nil {
		wd = filepath.Clean(workingDir)
	}
	p := filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(p) {
		// Git diff paths are repo-relative — anchor them to the working dir.
		p = filepath.Join(wd, p)
	}
	// #550 D1: an ABSOLUTE path must never be joined onto workingDir —
	// filepath.Join(wd, "/w/repo/x.go") silently produces the double
	// prefix "/w/repo/w/repo/x.go", so every normal edit of an
	// absolute-path file was misjudged "unreconciled" (systematic false
	// positives on the main workflow). Reduce both forms to the
	// repo-root-relative path via filepath.Rel instead.
	rel, rerr := filepath.Rel(wd, p)
	if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p // outside the working dir (or Rel failed): keep it absolute
	}
	return rel
}
