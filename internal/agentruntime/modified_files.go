package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
	"github.com/topcheer/ggcode/internal/vcs"
)

const (
	modifiedFilesMax     = 20
	modifiedFilesTimeout = 2 * time.Second
)

func modifiedFilesSection(workingDir string) string {
	return computeModifiedFilesSection(workingDir)
}

func computeModifiedFilesSection(workingDir string) string {
	if strings.TrimSpace(workingDir) == "" {
		return ""
	}

	v := vcs.Detect(workingDir)
	if v == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), modifiedFilesTimeout)
	defer cancel()

	clean, err := v.IsClean(ctx, workingDir)
	if err != nil || clean {
		return ""
	}

	status, err := v.Status(ctx, workingDir)
	if err != nil {
		return ""
	}
	// Trim trailing whitespace only: porcelain lines start with a status
	// column that may be a space (" M path"), and a leading TrimSpace would
	// cut the first line's column off ("M path" -> "path" parsed wrong).
	status = strings.TrimRight(status, " \t\r\n")
	if status == "" {
		return ""
	}

	// Harness state (.ggcode/: auto memory, checkpoints, usage stats,
	// skills) churns as a side effect of running and is often TRACKED, so it
	// shows up here as M/D on every turn. It is not the user's working-tree
	// change; with hundreds of such entries it also crowded the real changes
	// out of the 20-line window (field report 2026-11-26). Porcelain paths are
	// repo-root-relative, so this also drops the root .ggcode/ when the
	// working dir is a repository subdirectory.
	lines := util.FilterHarnessStatePorcelainLines(strings.Split(status, "\n"))
	total := len(lines)
	if total == 0 {
		return ""
	}

	shown := lines
	suffix := ""
	if total > modifiedFilesMax {
		shown = lines[:modifiedFilesMax]
		suffix = fmt.Sprintf("\n... (%d more)", total-modifiedFilesMax)
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Modified files\n")
	sb.WriteString("Files with uncommitted changes. Use git_status or git_diff for details.\n")
	for _, line := range shown {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if suffix != "" {
		sb.WriteString(suffix)
	}

	debug.Log("agentruntime", "modified files section: %d/%d files listed", len(shown), total)
	return strings.TrimRight(sb.String(), "\n")
}
