package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type commitHintState struct {
	fired bool
}

func newCommitHintState() *commitHintState {
	return &commitHintState{}
}

func (c *commitHintState) reset() {
	c.fired = false
}

func (a *Agent) checkCommitHintGate(runStats *RunStats) string {
	if a.commitHint == nil || a.commitHint.fired {
		return ""
	}
	a.commitHint.fired = true

	if !codeChangedInRun(runStats) {
		return ""
	}

	edited := runStats.FilesEdited
	if len(edited) == 0 {
		return ""
	}

	for toolName := range runStats.ToolCalls {
		if toolName == "git_commit" || toolName == "git_add" {
			return ""
		}
	}

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}

	status, err := gitStatusPorcelain(workingDir)
	if err != nil {
		debug.Log("commit-hint", "git status failed: %v", err)
		return ""
	}

	if status == "" {

		return ""
	}

	dirty := porcelainFilePaths(status)
	for i, p := range dirty {
		if !filepath.IsAbs(p) {
			dirty[i] = filepath.Join(workingDir, p)
		}
	}

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

func gitStatusPorcelain(workingDir string) (string, error) {
	output, err := runGitCommandWithTimeout(
		gitCommand(workingDir, "status", "--porcelain", "-uall"),
		gitDiffTimeout,
	)
	if err != nil {
		return "", err
	}
	return output, nil
}

func porcelainFilePaths(porcelain string) []string {
	var files []string
	for _, line := range strings.Split(porcelain, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		if len(line) < 4 {
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

func countChangedFiles(porcelain string) int {
	return len(porcelainFilePaths(porcelain))
}
