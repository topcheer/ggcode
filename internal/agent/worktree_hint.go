package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// #4995: a raw `git worktree add <path> <ref>` (no -b/-B) via run_command
// lands the worktree in a detached HEAD. The failure only surfaces later, at
// push time ("src refspec ... does not match any" / "HEAD is a commit
// object"), costing a recovery round-trip per session. Fail fast instead:
// hint at creation time with the exact recovery paths.
var (
	worktreeAddRe  = regexp.MustCompile(`\bgit\s+worktree\s+add\b`)
	worktreeFlagRe = regexp.MustCompile(`\s(-[bB]|--detach)\b`)
	detachedPushRe = regexp.MustCompile(`src refspec .* does not match any|fully qualify the ref`)
)

const worktreeDetachedGuidance = "[worktree-hint] git worktree add without -b creates a DETACHED HEAD worktree - commits will not land on any branch and push will fail late (#4995). Recovery: `git switch -c <branch-name>` then push, or push a fully-qualified refspec `HEAD:refs/heads/<branch>`. Prefer re-running with `git worktree add -b <branch> <path> <base>` or the enter_worktree tool."

// worktreeDetachedHint reports a guidance hint when either
//   - a raw `git worktree add` without a branch flag just SUCCEEDED
//     (creation-time fail-fast), or
//   - a command FAILED with the detached-HEAD push signature
//     (late-failure rescue).
func worktreeDetachedHint(toolName, command, resultContent string, isError bool) string {
	if toolName != "run_command" && toolName != "start_command" {
		return ""
	}
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ""
	}
	if !worktreeAddRe.MatchString(cmd) {
		// Late-failure rescue: push failing with the detached-HEAD signature.
		if isError && detachedPushRe.MatchString(resultContent) &&
			strings.HasPrefix(cmd, "git push") {
			return worktreeDetachedGuidance
		}
		return ""
	}
	// Raw worktree add with an explicit branch flag is fine.
	if worktreeFlagRe.MatchString(cmd) {
		return ""
	}
	if isError {
		return ""
	}
	return fmt.Sprintf("%s (command: %s)", worktreeDetachedGuidance, cmd)
}
