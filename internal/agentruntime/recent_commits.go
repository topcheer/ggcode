package agentruntime

import (
	"context"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/vcs"
)

const (
	recentCommitsMax     = 5
	recentCommitsTimeout = 2 * time.Second
)

func recentCommitsSection(workingDir string) string {
	return computeRecentCommitsSection(workingDir)
}

func computeRecentCommitsSection(workingDir string) string {
	if strings.TrimSpace(workingDir) == "" {
		return ""
	}

	v := vcs.Detect(workingDir)
	if v == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), recentCommitsTimeout)
	defer cancel()

	log, err := v.Log(ctx, workingDir, recentCommitsMax)
	if err != nil {
		return ""
	}
	log = strings.TrimSpace(log)
	if log == "" {
		return ""
	}

	lines := strings.Split(log, "\n")

	if len(lines) > recentCommitsMax {
		lines = lines[:recentCommitsMax]
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Recent commits\n")
	sb.WriteString("Latest commits in this repo for context. Use git_log for more details.\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	debug.Log("agentruntime", "recent commits section: %d entries", len(lines))
	return strings.TrimRight(sb.String(), "\n")
}
