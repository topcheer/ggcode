package agentruntime

import (
	"context"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/vcs"
)

// Recent-commits awareness for the system prompt.
//
// Research basis: Claude Code, Cursor, and GitHub Copilot Chat all give the
// agent visibility into recent git history. When the agent starts a new
// session or continues an existing one, knowing the last few commits helps
// it understand:
//
//   - What was recently changed and why (continuity across sessions)
//   - The commit message conventions used in this repo (style/format)
//   - The direction of recent development (feature areas being worked on)
//
// Without this, the agent would need to spend a tool call on git_log for
// the same information — one LLM round-trip per session that touches
// recently committed work.
//
// The section is intentionally compact: at most recentCommitsMax oneline
// entries (hash + subject), no body text, no author/email metadata.

const (
	// recentCommitsMax caps the number of commit entries shown.
	recentCommitsMax = 5
	// recentCommitsTimeout bounds the git log command so prompt
	// construction is never delayed by slow VCS operations.
	recentCommitsTimeout = 2 * time.Second
)

// recentCommitsSection returns a compact list of recent commits for
// injection into the system prompt. Returns an empty string if the
// directory is not a VCS repo, has no commits, or an error occurs.
//
// This function does I/O (git subprocess). In the interactive REPL it is
// called by the background SectionCollector, not by the prompt builder
// directly. The prompt builder reads the pre-computed value from the
// collector's snapshot. In pipe/test mode where no collector exists, it
// falls back to direct computation.
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
	// Defensive: trim in case VCS returned more than requested.
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
