package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/vcs"
)

// Modified-files awareness for the system prompt.
//
// Research basis: Claude Code, Cursor, and Windsurf all give the agent immediate
// visibility into which files have uncommitted changes — not just a count. The
// agent can then reason about the current working state (e.g., "the user has
// been editing planner.go and verify.go") without spending a tool call on
// git_status or git_diff. This saves one LLM round-trip per session that
// touches uncommitted work.
//
// ggcode already injects a one-line git summary ("5 uncommitted file(s)") via
// vcs.Summary, but the agent has no idea WHICH files changed. This module adds
// a compact "Modified files" section right after the git one-liner, listing
// up to N changed files with their status codes.

const (
	// modifiedFilesMax caps the number of files shown to keep the system prompt
	// small. If there are more, a "(N more)" suffix is appended.
	modifiedFilesMax = 20
	// modifiedFilesTimeout bounds the git status command so prompt construction
	// is never delayed by slow VCS operations.
	modifiedFilesTimeout = 2 * time.Second
)

// modifiedFilesSection returns a compact list of files with uncommitted changes
// for injection into the system prompt. Returns an empty string if the
// directory is not a VCS repo, the working tree is clean, or an error occurs.
//
// Each line uses the VCS's native short-status format (e.g., " M file.go"
// for git). This gives the agent file-level awareness without a tool call.
func modifiedFilesSection(workingDir string) string {
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
