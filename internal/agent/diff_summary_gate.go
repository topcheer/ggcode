package agent

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	diffSummaryMaxFiles = 15

	diffSummaryMinFiles = 2
)

type diffSummaryState struct {
	fired bool
}

func newDiffSummaryState() *diffSummaryState {
	return &diffSummaryState{}
}

func (d *diffSummaryState) reset() {
	d.fired = false
}

func (a *Agent) checkDiffSummaryGate(runStats *RunStats) string {
	if a.diffSummary == nil || a.diffSummary.fired {
		return ""
	}
	a.diffSummary.fired = true

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}

	if !codeChangedInRun(runStats) {
		return ""
	}

	stat, err := gitDiffStat(workingDir)
	if err != nil {
		debug.Log("diff-summary", "git diff --stat failed: %v", err)
		return ""
	}

	lines := strings.Split(strings.TrimSpace(stat), "\n")
	var fileLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

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

func gitDiffStat(workingDir string) (string, error) {
	output, err := runGitCommandWithTimeout(
		gitCommand(workingDir, "diff", "--stat", "HEAD"),
		gitDiffTimeout,
	)
	if err != nil {

		output, err = runGitCommandWithTimeout(
			gitCommand(workingDir, "diff", "--stat"),
			gitDiffTimeout,
		)
		if err != nil {
			return "", err
		}
	}

	if st, err := runGitCommandWithTimeout(
		gitCommand(workingDir, "status", "--porcelain"),
		gitDiffTimeout,
	); err == nil {
		for _, line := range strings.Split(st, "\n") {
			if strings.HasPrefix(line, "??") {
				output += "\n" + strings.TrimSpace(line[2:]) + " | 0 +"
			}
		}
	}
	return output, nil
}

func shortenPathForDisplay(workingDir, line string) string {

	parts := strings.SplitN(line, "|", 2)
	if len(parts) < 2 {
		return line
	}
	filePath := strings.TrimSpace(parts[0])
	if abs, err := filepath.Abs(filepath.Join(workingDir, filePath)); err == nil {

		if strings.HasPrefix(filePath, workingDir) {
			rel, err := filepath.Rel(workingDir, abs)
			if err == nil {
				return rel + " |" + parts[1]
			}
		}
	}
	return line
}

func gitCommand(workingDir string, args ...string) *exec.Cmd {
	return exec.Command("git", append([]string{"-C", workingDir}, args...)...)
}
