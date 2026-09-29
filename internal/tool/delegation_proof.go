package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/subagent"
)

// Delegation proof-of-work implements the "delegate, then verify" protocol
// (frontier multi-agent practice, 2025-2026): a delegator should not trust a
// sub-agent's self-reported summary - it should require evidence. When a run
// executes in an isolation worktree, its actual effects are cheap to verify
// with git: branch tip, last commit, and working-tree state. This appendix is
// attached to wait_agent results for terminal snapshots so the parent agent
// can gate adoption of the delegated work on verified facts instead of the
// child's claims alone. It is evidence collection, not a quality detector.
const (
	delegationProofCmdTimeout = 10 * time.Second
	// delegationProofMaxStatLines caps per-file diffstat lines; the summary
	// line (files changed / insertions / deletions) is always kept.
	delegationProofMaxStatLines = 8
	// delegationProofMaxStatusLines caps per-file working-tree status lines.
	delegationProofMaxStatusLines = 5
	// delegationProofMaxSubject caps the last-commit subject length.
	delegationProofMaxSubject = 80
)

// gitWorktreeOutput runs a git command scoped to an isolation worktree and
// returns trimmed combined output. A hard timeout keeps a wedged worktree
// (e.g. locked index on a network volume) from blocking the agent loop.
func gitWorktreeOutput(ctx context.Context, worktree string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, delegationProofCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", append([]string{"-C", worktree}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// terminalSnapshot reports whether a sub-agent run reached a terminal state,
// i.e. its worktree will no longer change under us. Proof is only collected
// for terminal snapshots: polling wait_agent on a running run must stay cheap.
func terminalSnapshot(st subagent.Status) bool {
	switch st {
	case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled:
		return true
	}
	return false
}

// buildWorktreeProof collects verifiable evidence about what a delegated run
// left behind in its isolation worktree. Every fact is read from git, never
// from the child's self-reported result. Failures degrade gracefully to a
// "proof unavailable" note so the parent still learns the worktree exists.
func buildWorktreeProof(ctx context.Context, snap subagent.Snapshot) string {
	var b strings.Builder
	b.WriteString("Proof-of-work (collected from the worktree, not self-reported):")

	branch, err := gitWorktreeOutput(ctx, snap.Worktree, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		fmt.Fprintf(&b, "\n- proof unavailable: git rev-parse failed: %v", err)
		return b.String()
	}
	sha, _ := gitWorktreeOutput(ctx, snap.Worktree, "rev-parse", "--short", "HEAD")
	subject, _ := gitWorktreeOutput(ctx, snap.Worktree, "log", "-1", "--format=%s")
	fmt.Fprintf(&b, "\n- branch: %s @ %s", branch, sha)
	if subject != "" {
		fmt.Fprintf(&b, " (last commit: %s)", truncateProofLine(subject, delegationProofMaxSubject))
	}

	if status, err := gitWorktreeOutput(ctx, snap.Worktree, "status", "--porcelain"); err == nil {
		if status == "" {
			b.WriteString("\n- working tree: clean (all changes committed)")
		} else {
			lines := strings.Split(status, "\n")
			fmt.Fprintf(&b, "\n- working tree: %d uncommitted change(s)", len(lines))
			for i, l := range lines {
				if i >= delegationProofMaxStatusLines {
					fmt.Fprintf(&b, "\n  ... (%d more)", len(lines)-delegationProofMaxStatusLines)
					break
				}
				fmt.Fprintf(&b, "\n  %s", strings.TrimSpace(l))
			}
		}
	}

	if stat, err := gitWorktreeOutput(ctx, snap.Worktree, "diff", "--stat", "HEAD"); err == nil && stat != "" {
		lines := strings.Split(stat, "\n")
		summary := strings.TrimSpace(lines[len(lines)-1])
		files := lines[:len(lines)-1]
		if summary != "" {
			fmt.Fprintf(&b, "\n- uncommitted diff: %s", summary)
		}
		for i, l := range files {
			if i >= delegationProofMaxStatLines {
				fmt.Fprintf(&b, "\n  ... (%d more file(s))", len(files)-delegationProofMaxStatLines)
				break
			}
			fmt.Fprintf(&b, "\n  %s", strings.TrimSpace(l))
		}
	}

	b.WriteString("\nAdopt explicitly after review - the isolation worktree is not merged automatically.")
	return b.String()
}

// truncateProofLine shortens s to at most max runes, marking the cut.
func truncateProofLine(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}
