package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
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
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}

	lines := strings.Split(status, "\n")
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
