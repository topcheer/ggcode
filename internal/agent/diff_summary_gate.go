package agent

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
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

	// Drop harness state and git-ignored paths BEFORE the min-files check:
	// they are not the agent's task changes, and listing them only adds noise.
	fileLines = filterDiffSummaryNoise(workingDir, fileLines)

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
	sb.WriteString("[Self-review: you are about to finish. Here is a summary of your changes (git diff --stat; .ggcode/ and git-ignored paths excluded). ")
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

// harnessStateDirPrefixes are maintained by the harness itself as a side
// effect of running (refreshed on tool calls), never by the task.
var harnessStateDirPrefixes = []string{".ggcode/"}

func isHarnessStatePath(path string) bool {
	return util.IsHarnessStatePath(path)
}

// filterDiffSummaryNoise drops diff-stat lines whose path is harness state
// or matches a .gitignore rule. git diff lists TRACKED files, which gitignore
// does not cover, so a previously committed .ggcode/ or build/ file would
// otherwise show up as "your change" on every turn.
func filterDiffSummaryNoise(workingDir string, lines []string) []string {
	kept := make([]string, 0, len(lines))
	var candidates []string
	for _, line := range lines {
		path := strings.TrimSpace(strings.SplitN(line, "|", 2)[0])
		if isHarnessStatePath(path) {
			continue
		}
		kept = append(kept, line)
		candidates = append(candidates, path)
	}
	ignored := gitIgnoredPaths(workingDir, candidates)
	if len(ignored) == 0 {
		return kept
	}
	out := kept[:0]
	for _, line := range kept {
		path := strings.TrimSpace(strings.SplitN(line, "|", 2)[0])
		if ignored[path] {
			continue
		}
		out = append(out, line)
	}
	return out
}

// gitIgnoredPaths returns the subset of paths matched by any .gitignore rule
// (--no-index: applies to tracked files too). Best effort: on any git error
// nothing is filtered.
func gitIgnoredPaths(workingDir string, paths []string) map[string]bool {
	if len(paths) == 0 {
		return nil
	}
	cmd := gitCommand(workingDir, "check-ignore", "--no-index", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	out, err := runGitCommandWithTimeout(cmd, gitDiffTimeout)
	if err != nil {
		// exit status 1 = no path ignored; anything else is not worth failing on
		return nil
	}
	ignored := make(map[string]bool)
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ignored[l] = true
		}
	}
	return ignored
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
