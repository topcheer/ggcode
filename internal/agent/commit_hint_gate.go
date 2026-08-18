package agent

// Post-Completion Commit Hint Gate
//
// Research basis: Aider auto-commits after every edit. Claude Code auto-commits
// at the end of each task. Cursor and Windsurf both prompt the user to commit
// when changes are ready. The pattern across all major AI coding agents is:
// changes should not be left uncommitted in the working tree when a task ends.
//
// The gap in ggcode: the agent has a diff summary self-review gate and a change
// reconciliation gate, but neither checks whether the agent actually committed
// its work. After all gates pass, the agent simply returns, often leaving
// changed files unstaged and uncommitted. The user then has to manually run
// git add + git commit, or the changes get lost when the next task starts.
//
// This gate fires AFTER all other completion gates pass (it is the last gate
// before the agent returns). It checks:
//   1. The agent made code edits in this run (not just exploration/Q&A)
//   2. The agent did NOT already call git_add or git_commit
//   3. There are uncommitted changes in the working tree
//
// When all three conditions are met, it injects a system message reminding the
// agent to stage and commit its work. This is advisory (non-blocking); it does
// not force a commit, but gives the agent one final opportunity to wrap up
// properly before returning to the user.
//
// Zero LLM cost. The git status check completes in <50ms.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// commitHintState tracks whether the commit hint gate has fired this run.
type commitHintState struct {
	fired bool
}

func newCommitHintState() *commitHintState {
	return &commitHintState{}
}

func (c *commitHintState) reset() {
	c.fired = false
}

// gitCommitTools are the tools the agent uses to stage and commit changes.
// If any of these were called during the run, the agent already handled
// version control; no hint needed. git_stash is exempted only because a
// stash is an explicit decision to park changes — but see the stash-pop
// note in checkCommitHintGate (#698).

// checkCommitHintGate checks whether the agent should be reminded to commit.
// Returns a non-empty message if the agent made edits but hasn't committed them.
// Returns empty string when:
//   - the gate already fired this run
//   - no code was edited in this run
//   - the agent already used git_add or git_commit
//   - the agent's edited files are all committed (no dirt attributable to it)
//   - the working directory is not set or not a git repo
//
// #698: the agent's blast radius is RunStats.FilesEdited — never the whole
// tree's porcelain line count. In a shared workspace the tree carries
// pre-existing user changes and other agents' untracked files; attributing
// those to the agent ("You made changes to N files") sent it hunting for
// edits it never made. The hint now scopes to the agent's own edit list and
// separately discloses unrelated tree dirt.
func (a *Agent) checkCommitHintGate(runStats *RunStats) string {
	if a.commitHint == nil || a.commitHint.fired {
		return ""
	}
	a.commitHint.fired = true

	// Only hint when the agent made actual source-code edits.
	if !codeChangedInRun(runStats) {
		return ""
	}

	// The agent's own edits are the hint's scope. Without per-file data there
	// is nothing attributable to the agent — do not claim the whole tree.
	edited := runStats.FilesEdited
	if len(edited) == 0 {
		return ""
	}

	// If the agent already staged or committed, it handled version control.
	// #698: git_stash is NOT exempt — an edit → git_stash → git_stash pop
	// round trip leaves the agent's changes uncommitted in the tree, which is
	// exactly the situation this gate exists to flag.
	for toolName := range runStats.ToolCalls {
		if toolName == "git_commit" || toolName == "git_add" {
			return ""
		}
	}

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}

	// Check for uncommitted changes (staged or unstaged).
	status, err := gitStatusPorcelain(workingDir)
	if err != nil {
		debug.Log("commit-hint", "git status failed: %v", err)
		return ""
	}

	if status == "" {
		// No uncommitted changes — nothing to hint about.
		return ""
	}

	// #698: only the intersection of the agent's edit list with the dirty
	// tree is attributable to the agent. Files it edited but since committed
	// (e.g. via run_command) are clean and excluded automatically. Porcelain
	// paths are repo-relative while FilesEdited are absolute — normalize
	// before intersecting.
	dirty := porcelainFilePaths(status)
	for i, p := range dirty {
		if !filepath.IsAbs(p) {
			dirty[i] = filepath.Join(workingDir, p)
		}
	}
	// #705: FilesEdited records the LLM's literal pre-execution tool argument,
	// which is relative in the most common usage (edit_file {path: "src/foo.go"}
	// resolves against the tool's WorkingDir at execution time, but the record
	// side keeps the literal string). Absolutize both sides against workingDir
	// or the intersection is always empty and the #698 fix is inert.
	editedAbs := make([]string, len(edited))
	for i, p := range edited {
		if !filepath.IsAbs(p) {
			editedAbs[i] = filepath.Join(workingDir, p)
		} else {
			editedAbs[i] = p
		}
	}
	mine := intersectFileSets(editedAbs, dirty)
	if len(mine) == 0 {
		// None of the agent's edits are dirty — the tree's dirt belongs to
		// someone else; never urge the agent to commit it.
		debug.Log("commit-hint", "skipped: none of the agent's %d edited files are dirty", len(edited))
		return ""
	}
	others := len(dirty) - len(mine)

	debug.Log("commit-hint", "injected commit reminder: %d agent-edited files uncommitted (%d unrelated dirty)", len(mine), others)

	var sb strings.Builder
	sb.WriteString("[Post-completion reminder: You edited ")
	if len(mine) == 1 {
		sb.WriteString("1 file")
	} else {
		sb.WriteString(fmt.Sprintf("%d files", len(mine)))
	}
	sb.WriteString(" that are not staged or committed: ")
	sb.WriteString(strings.Join(shortenFileList(mine), ", "))
	sb.WriteString(". ")
	if others > 0 {
		sb.WriteString(fmt.Sprintf("Note: the working tree also has %d unrelated pre-existing/untracked change(s) you did NOT make — leave those alone. ", others))
	}
	sb.WriteString("If the task is complete, stage the relevant files with git_add and commit with git_commit. ")
	sb.WriteString("Use a clear, conventional commit message (e.g. 'feat:', 'fix:', 'refactor:'). ")
	sb.WriteString("Scope the commit to the files listed above only; do NOT use 'git add -A' or 'git commit -a'.]")

	return sb.String()
}

// gitStatusPorcelain runs `git status --porcelain` and returns the raw output.
func gitStatusPorcelain(workingDir string) (string, error) {
	output, err := runGitCommandWithTimeout(
		gitCommand(workingDir, "status", "--porcelain"),
		gitDiffTimeout,
	)
	if err != nil {
		return "", err
	}
	return output, nil
}

// porcelainFilePaths extracts the changed file paths from `git status
// --porcelain` output. Each line is "XY filename" or "XY original -> renamed";
// for renames only the destination (post ->) path is kept — that is where the
// change lives now.
func porcelainFilePaths(porcelain string) []string {
	var files []string
	for _, line := range strings.Split(porcelain, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Porcelain v1: "XY path" — X at [0], Y at [1], space at [2]. Do NOT
		// TrimSpace the line first: a leading-space X (" M file") would shift
		// the slice and eat the first character of the path.
		if len(line) < 4 { // "XY " + at least one char
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if path != "" {
			files = append(files, path)
		}
	}
	return files
}

// intersectFileSets returns the paths present in both lists, preserving the
// order of edited. Both sides are normalized (cleaned + absolutized against
// workingDir by the caller when needed); here plain string match on the
// cleaned path.
func intersectFileSets(edited, dirty []string) []string {
	dirtySet := make(map[string]bool, len(dirty))
	for _, p := range dirty {
		dirtySet[filepath.Clean(p)] = true
	}
	var out []string
	for _, p := range edited {
		if dirtySet[filepath.Clean(p)] {
			out = append(out, p)
		}
	}
	return out
}

// shortenFileList trims each path to a display-friendly form (leading "./"
// and workspace prefixes removed) and caps the list length.
func shortenFileList(files []string) []string {
	const maxListed = 10
	out := make([]string, 0, len(files))
	for i, f := range files {
		if i == maxListed {
			out = append(out, fmt.Sprintf("... and %d more", len(files)-maxListed))
			break
		}
		out = append(out, f)
	}
	return out
}

// countChangedFiles counts the number of changed files from `git status --porcelain`
// output. Each line represents one file (or two for renames). Format:
//
//	XY filename
//	XY original -> renamed
//
// We count one file per line, treating renames as a single file change.
func countChangedFiles(porcelain string) int {
	return len(porcelainFilePaths(porcelain))
}
