package agent

// Auto-Commit (config "auto_commit")
//
// Research basis: Aider's defining feature is git-native auto-commit — every
// change lands as an atomic commit with a descriptive message and explicit
// attribution (https://aider.chat/docs/git.html). 2026 terminal-agent
// comparisons frame this as one of two mainstream commit policies: Aider
// commits every change as it happens; Claude Code auto-commits at the end of
// each task (see the research notes atop commit_hint_gate.go). ggcode
// historically only *advised* the model to commit (the advisory hint gate);
// that leaves uncommitted work behind whenever the model ignores the
// reminder.
//
// This file implements the opt-in end-of-run commit: when auto_commit is
// enabled and the commit gate fires, the agent's attributable files (the
// #698/#705 intersection of RunStats.FilesEdited with the dirty tree) are
// staged and committed exactly, leaving unrelated working-tree changes
// untouched. Any git failure falls back to the advisory hint — auto-commit
// never breaks a run and is disabled by default.

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// gitCommitTimeout is more generous than gitDiffTimeout: user commit
	// hooks may legitimately run quick checks.
	gitCommitTimeout = 30 * time.Second
	// autoCommitAddBatch bounds argv size when staging many files.
	autoCommitAddBatch = 50
	// autoCommitSubjectMax caps the commit subject length.
	autoCommitSubjectMax = 60
)

// tryAutoCommit stages exactly files and commits them. On success it returns
// an informational note for the model (the note prevents a redundant commit
// attempt on the extra turn the injection creates). It returns "" on any
// failure or when auto-commit is disabled — the caller then falls back to the
// advisory hint unchanged.
func (a *Agent) tryAutoCommit(runStats *RunStats, files []string) string {
	if !a.autoCommit || len(files) == 0 {
		return ""
	}
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}
	for start := 0; start < len(files); start += autoCommitAddBatch {
		end := min(start+autoCommitAddBatch, len(files))
		args := append([]string{"add", "--"}, files[start:end]...)
		if _, err := runGitCommandWithTimeout(gitCommand(workingDir, args...), gitDiffTimeout); err != nil {
			debug.Log("auto-commit", "git add failed: %v", err)
			return ""
		}
	}
	subject := autoCommitSubject(runStats)
	body := fmt.Sprintf("Auto-committed by ggcode (auto_commit): %d attributable file(s) staged exclusively; unrelated working-tree changes left untouched.\n\nCo-Authored-By: ggcode <noreply@ggcode.dev>", len(files))
	// Commit with an explicit pathspec: a plain `git commit` would also sweep
	// in changes the USER had already staged before the run. The pathspec
	// restricts the commit to exactly the attributable files; everything else
	// (staged or not) keeps its pre-commit index/worktree state. The preceding
	// `git add` is what makes untracked attributable files match the pathspec.
	commitArgs := append([]string{"commit", "-m", subject, "-m", body, "--"}, files...)
	if _, err := runGitCommandWithTimeout(gitCommand(workingDir, commitArgs...), gitCommitTimeout); err != nil {
		debug.Log("auto-commit", "git commit failed: %v", err)
		return ""
	}
	hash, err := runGitCommandWithTimeout(gitCommand(workingDir, "rev-parse", "--short", "HEAD"), gitDiffTimeout)
	if err != nil {
		debug.Log("auto-commit", "rev-parse failed: %v", err)
		hash = ""
	}
	hash = strings.TrimSpace(hash)
	debug.Log("auto-commit", "committed %d file(s) as %s", len(files), hash)

	var sb strings.Builder
	sb.WriteString("[auto-commit] ")
	if len(files) == 1 {
		sb.WriteString("1 file")
	} else {
		sb.WriteString(fmt.Sprintf("%d files", len(files)))
	}
	sb.WriteString(" you edited were staged and committed")
	if hash != "" {
		sb.WriteString(fmt.Sprintf(" as %s", hash))
	}
	sb.WriteString(": ")
	sb.WriteString(strings.Join(shortenFileList(files), ", "))
	sb.WriteString(". Do not stage or commit them again. Unrelated working-tree changes were NOT included.]")
	return sb.String()
}

// autoCommitSubject derives a commit subject from the run's user prompt:
// first line only, backticks flattened, rune-safe truncation. Falls back to
// a generic subject when the prompt is empty.
func autoCommitSubject(runStats *RunStats) string {
	s := ""
	if runStats != nil {
		s = runStats.UserPrompt
	}
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\n\r"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	s = strings.ReplaceAll(s, "`", "'")
	if s == "" {
		return "ggcode: apply requested changes"
	}
	runes := []rune(s)
	if len(runes) > autoCommitSubjectMax {
		s = strings.TrimSpace(string(runes[:autoCommitSubjectMax])) + "..."
	}
	return s
}
