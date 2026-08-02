package agent

// Diff Summary Self-Review Gate
//
// Research basis: Claude Code, Cursor Composer, and Windsurf all surface a
// consolidated view of all changes before the agent declares "done." This gives
// the LLM a final opportunity to self-review its work holistically, catching
// incomplete edits, accidental deletions, or leftover debug code that per-edit
// diffs (compactDiff) and individual tool results don't reveal.
//
// The gap in ggcode: existing systems operate at different levels:
//   - compactDiff: shows per-edit line-level diffs (micro view)
//   - changeReconcile: only flags UNEXPECTED side-effect files (negative check)
//   - fulfillmentGate: checks IF work was done (boolean check)
//   - scopeDrift: warns when too many files are edited (quantity check)
//
// None of them provide the agent with a holistic "here's everything you changed"
// summary for self-review. This gate fills that gap by running `git diff --stat`
// and injecting a compact per-file change summary (insertions/deletions) before
// the agent returns, but ONLY when:
//   1. The agent made source-code edits (not just exploration/Q&A)
//   2. No other gate caused a continue (this is the last gate before exit)
//   3. The summary hasn't been shown yet this run
//
// Zero LLM cost. The git diff --stat command completes in <50ms even for
// large repos, and the output is compact (one line per changed file).

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// diffSummaryMaxFiles caps the number of files shown to keep the message
	// compact. If more files changed, a "(N more)" suffix is appended.
	diffSummaryMaxFiles = 15

	// diffSummaryMinFiles: only inject the summary when at least this many
	// files were edited. For single-file edits, the per-edit diff is sufficient.
	diffSummaryMinFiles = 2
)

// diffSummaryState tracks whether the gate has fired this run.
type diffSummaryState struct {
	fired bool
}

func newDiffSummaryState() *diffSummaryState {
	return &diffSummaryState{}
}

func (d *diffSummaryState) reset() {
	d.fired = false
}

// checkDiffSummaryGate runs `git diff --stat HEAD` and returns a compact
// change summary for the agent to self-review. Returns empty string when:
//   - the gate already fired this run
//   - fewer than diffSummaryMinFiles were changed
//   - not a git repo or git command fails
//   - the working directory is empty
func (a *Agent) checkDiffSummaryGate(runStats *RunStats) string {
	if a.diffSummary == nil || a.diffSummary.fired {
		return ""
	}
	a.diffSummary.fired = true

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}

	// Only show summary when the agent made actual source-code edits.
	if !codeChangedInRun(runStats) {
		return ""
	}

	stat, err := gitDiffStat(workingDir)
	if err != nil {
		debug.Log("diff-summary", "git diff --stat failed: %v", err)
		return ""
	}

	lines := strings.Split(strings.TrimSpace(stat), "\n")
	// Filter to non-empty lines and exclude pure-whitespace/side-effect entries.
	var fileLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// The final summary line from git looks like "N files changed, ..."
		// We include it separately, not as a "file."
		if strings.Contains(line, "files changed") || strings.Contains(line, "file changed") {
			continue
		}
		fileLines = append(fileLines, line)
	}

	if len(fileLines) < diffSummaryMinFiles {
		return ""
	}

	shown := fileLines
	suffix := ""
	if len(shown) > diffSummaryMaxFiles {
		shown = shown[:diffSummaryMaxFiles]
		suffix = fmt.Sprintf("\n  ... and %d more", len(fileLines)-diffSummaryMaxFiles)
	}

	var sb strings.Builder
	sb.WriteString("[Self-review: you are about to finish. Here is a summary of ALL your changes (git diff --stat). ")
	sb.WriteString("Review this list, verify each change is intentional, complete, and matches the user's request.]\n")
	for _, line := range shown {
		// Shorten absolute paths to repo-relative for readability.
		line = shortenPathForDisplay(workingDir, line)
		sb.WriteString("  ")
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if suffix != "" {
		sb.WriteString(suffix)
		sb.WriteByte('\n')
	}

	debug.Log("diff-summary", "injected self-review summary: %d files", len(fileLines))
	return strings.TrimRight(sb.String(), "\n")
}

// gitDiffStat runs `git diff --stat HEAD` and returns the raw output.
func gitDiffStat(workingDir string) (string, error) {
	output, err := runGitCommandWithTimeout(
		gitCommand(workingDir, "diff", "--stat", "HEAD"),
		gitDiffTimeout,
	)
	if err != nil {
		// Fall back to unstaged-only diff (new repo with no commits).
		output, err = runGitCommandWithTimeout(
			gitCommand(workingDir, "diff", "--stat"),
			gitDiffTimeout,
		)
		if err != nil {
			return "", err
		}
	}
	return output, nil
}

// shortenPathForDisplay converts absolute paths in git output to repo-relative.
func shortenPathForDisplay(workingDir, line string) string {
	// git diff --stat output format: " path/to/file.go | 15 +++--"
	// We want to strip the working directory prefix if present.
	parts := strings.SplitN(line, "|", 2)
	if len(parts) < 2 {
		return line
	}
	filePath := strings.TrimSpace(parts[0])
	if abs, err := filepath.Abs(filepath.Join(workingDir, filePath)); err == nil {
		// If the path in the output is absolute, make it relative.
		if strings.HasPrefix(filePath, workingDir) {
			rel, err := filepath.Rel(workingDir, abs)
			if err == nil {
				return rel + " |" + parts[1]
			}
		}
	}
	return line
}

// gitCommand creates an exec.Cmd for a git command in the given directory.
func gitCommand(workingDir string, args ...string) *exec.Cmd {
	return exec.Command("git", append([]string{"-C", workingDir}, args...)...)
}
