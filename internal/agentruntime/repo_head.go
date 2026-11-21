package agentruntime

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/vcs"
)

// lastLoggedRepoHead suppresses repeated repo-head log lines when the
// periodic collector refreshes without a change (ring-noise control).
var lastLoggedRepoHead string

const repoHeadSectionTimeout = 2 * time.Second

func repoHeadSection(workingDir string) string {
	return computeRepoHeadSection(workingDir)
}

// computeRepoHeadSection renders the single-line repo HEAD marker.
//
// Deliberately NOT a commit-message list: the old "Recent commits" block was
// volatile, frozen-at-build content that was routinely misread as current
// main state (shared-checkout-context-snapshot-trap) and invalidated the
// system-prompt cache tail on every merge. One stable-format line keeps the
// change-detection signal (branch@sha + upstream behind-count) while commit
// context stays on-demand via the git_log tool (#5370).
func computeRepoHeadSection(workingDir string) string {
	if strings.TrimSpace(workingDir) == "" {
		return ""
	}

	v := vcs.Detect(workingDir)
	if v == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), repoHeadSectionTimeout)
	defer cancel()

	branch, err := v.CurrentBranch(ctx, workingDir)
	if err != nil {
		return ""
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return ""
	}

	rev := ""
	if log, err := v.Log(ctx, workingDir, 1); err == nil {
		if first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(log), "\n", 2)[0]); first != "" {
			if fields := strings.Fields(first); len(fields) > 0 {
				rev = fields[0]
			}
		}
	}

	behind := 0
	if g, ok := v.(vcs.Git); ok {
		if _, b, okBehind := g.AheadBehind(ctx, workingDir); okBehind && b > 0 {
			behind = b
		}
	}

	ref := branch
	if rev != "" {
		if branch == rev {
			ref = "detached@" + rev
		} else {
			ref = branch + "@" + rev
		}
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Repo HEAD\n")
	sb.WriteString("repo HEAD at session start: ")
	sb.WriteString(ref)
	if behind > 0 {
		sb.WriteString(" (")
		sb.WriteString(strconv.Itoa(behind))
		sb.WriteString(" behind upstream)")
	}
	sb.WriteString(" — commit context: use git_log\n")

	// Ring-noise control: this collector re-fires every ~10s; log only on change.
	if ref != lastLoggedRepoHead {
		lastLoggedRepoHead = ref
		debug.Log("agentruntime", "repo head section: %s", ref)
	}
	return strings.TrimRight(sb.String(), "\n")
}
