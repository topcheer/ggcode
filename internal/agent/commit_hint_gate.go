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
// version control; no hint needed.
var gitCommitTools = map[string]bool{
	"git_commit": true,
	"git_add":    true,
	"git_stash":  true, // stashing is also a valid way to handle changes
}

// checkCommitHintGate checks whether the agent should be reminded to commit.
// Returns a non-empty message if the agent made edits but hasn't committed them.
// Returns empty string when:
//   - the gate already fired this run
//   - no code was edited in this run
//   - the agent already used git_add, git_commit, or git_stash
//   - there are no uncommitted changes in the working tree
//   - the working directory is not set or not a git repo
func (a *Agent) checkCommitHintGate(runStats *RunStats) string {
	if a.commitHint == nil || a.commitHint.fired {
		return ""
	}
	a.commitHint.fired = true

	// Only hint when the agent made actual source-code edits.
	if !codeChangedInRun(runStats) {
		return ""
	}

	// If the agent already staged or committed, it handled version control.
	for toolName := range runStats.ToolCalls {
		if gitCommitTools[toolName] {
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

	// Count modified/added files from porcelain output.
	fileCount := countChangedFiles(status)
	if fileCount == 0 {
		return ""
	}

	debug.Log("commit-hint", "injected commit reminder: %d changed files uncommitted", fileCount)

	var sb strings.Builder
	sb.WriteString("[Post-completion reminder: You made changes to ")
	if fileCount == 1 {
		sb.WriteString("1 file")
	} else {
		sb.WriteString(fmt.Sprintf("%d files", fileCount))
	}
	sb.WriteString(" but have not staged or committed them. ")
	sb.WriteString("If the task is complete, stage the relevant files with git_add and commit with git_commit. ")
	sb.WriteString("Use a clear, conventional commit message (e.g. 'feat:', 'fix:', 'refactor:'). ")
	sb.WriteString("Do NOT use 'git add -A' or 'git commit -a' if there are pre-existing uncommitted changes from the user.]")

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

// countChangedFiles counts the number of changed files from `git status --porcelain`
// output. Each line represents one file (or two for renames). Format:
//
//	XY filename
//	XY original -> renamed
//
// We count one file per line, treating renames as a single file change.
func countChangedFiles(porcelain string) int {
	count := 0
	for _, line := range strings.Split(porcelain, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 {
			continue
		}
		// Skip untracked files only if they're not from agent edits, but
		// in porcelain format, "??" means untracked. We still count them
		// because the agent may have created new files.
		count++
	}
	return count
}
