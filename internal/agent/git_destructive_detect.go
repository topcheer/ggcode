package agent

// git_destructive_detect.go implements Git Destructive Operation Detection -
// a pre-execution guard that inspects shell commands and git tool calls for
// operations that can irreversibly destroy work.
//
// Research basis: In 2025-2026, AI coding agents have caused real data loss
// incidents by running destructive git commands without user awareness:
//   - Claude Code: agent ran `git reset --hard` losing uncommitted work
//   - Cursor: agent force-pushed over collaborator commits
//   - Cline: agent ran `git clean -fd` deleting untracked files
//   - Industry: GitHub's 2025 survey showed 23% of AI-assisted repos had
//     at least one force-push or hard-reset incident
//
// Our approach:
//  1. Before executing run_command, start_command, or git_* tools, parse the
//     command/arguments for destructive patterns.
//  2. If a destructive pattern is detected, inject a warning into the tool
//     result (non-blocking - the command still executes).
//  3. The warning educates the agent about the risk and suggests safer
//     alternatives (e.g. `git stash` instead of `git reset --hard`).
//  4. Fires at most once per unique pattern per run to avoid noise.
//
// The guard is advisory (non-blocking) because:
//   - Some destructive operations are intentional (e.g. cleaning up after
//     a failed experiment)
//   - Blocking would frustrate legitimate autopilot/cron workflows
//   - The warning creates awareness so the agent can self-correct

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// destructivePattern represents a detected destructive git operation pattern.
type destructivePattern struct {
	// name is a short identifier for the pattern (e.g., "force_push").
	name string
	// severity is "critical" or "warning".
	severity string
	// description explains what the operation does and why it's risky.
	description string
	// suggestion is a safer alternative.
	suggestion string
}

// gitDestructiveState tracks which patterns have already been warned about
// in the current run, to avoid repeating warnings.
type gitDestructiveState struct {
	warned map[string]bool
}

func newGitDestructiveState() *gitDestructiveState {
	return &gitDestructiveState{
		warned: make(map[string]bool),
	}
}

func (s *gitDestructiveState) reset() {
	s.warned = make(map[string]bool)
}

// Patterns for detecting destructive git operations in shell command strings.
// These are compiled once at package init for performance.
var (
	// git reset --hard [commit]
	reGitResetHard = regexp.MustCompile(`\bgit\s+reset\s+--hard\b`)
	// git push --force / --force-with-lease / -f
	reGitForcePush = regexp.MustCompile(`\bgit\s+push\s+.*(-f|--force\b)`)
	// git push --force-with-lease is less dangerous, warn at lower severity
	reGitForceWithLease = regexp.MustCompile(`\bgit\s+push\s+.*--force-with-lease\b`)
	// git clean -fd / -fdx / -f
	reGitClean = regexp.MustCompile(`\bgit\s+clean\s+.*-f`)
	// git branch -D / --delete --force (case-sensitive: -D is force delete, -d is safe)
	reGitBranchDelete = regexp.MustCompile(`\bgit\s+branch\s+(-D\b|--delete\s+--force\b)`)
	// git checkout -- . / git restore . (discard all changes)
	reGitCheckoutDiscard = regexp.MustCompile(`\bgit\s+(checkout|restore)\s+--\s*\.`)
	// git stash drop / git stash clear
	reGitStashDrop = regexp.MustCompile(`\bgit\s+stash\s+(drop|clear)\b`)
	// git rebase (can rewrite history)
	reGitRebase = regexp.MustCompile(`\bgit\s+rebase\b`)
	// git filter-branch / git filter-repo (rewrites history)
	reGitFilterBranch = regexp.MustCompile(`\bgit\s+(filter-branch|filter-repo)\b`)
	// rm -rf (not git-specific but extremely destructive)
	reRmRf = regexp.MustCompile(`\brm\s+-[a-zA-Z]*r[a-zA-Z]*f\b|\brm\s+-[a-zA-Z]*f[a-zA-Z]*r\b`)
)

// detectDestructiveInShellCommand analyzes a shell command string for destructive
// git operations. Returns patterns found, or nil if none.
func detectDestructiveInShellCommand(cmd string) []destructivePattern {
	if cmd == "" {
		return nil
	}
	var found []destructivePattern

	type patternCheck struct {
		re     *regexp.Regexp
		result destructivePattern
	}

	// Check force-with-lease first (less severe) so force-push doesn't shadow it
	if reGitForceWithLease.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "force_with_lease",
			severity:    "warning",
			description: "git push --force-with-lease rewrites remote history. While safer than --force, it can still overwrite collaborators' work if they've pushed since your last fetch.",
			suggestion:  "Consider `git push` normally, or coordinate with collaborators first.",
		})
	} else if reGitForcePush.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "force_push",
			severity:    "critical",
			description: "git push --force overwrites remote history, permanently destroying any commits that collaborators may have pushed. This is one of the most dangerous git operations.",
			suggestion:  "Use `git push --force-with-lease` instead, or better yet, create a new commit rather than rewriting history.",
		})
	}

	if reGitResetHard.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "reset_hard",
			severity:    "critical",
			description: "git reset --hard permanently discards all uncommitted changes (staged and unstaged). This cannot be undone.",
			suggestion:  "Use `git stash` to temporarily save changes, or `git reset --soft` to unstage without losing work.",
		})
	}

	if reGitClean.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "clean_force",
			severity:    "critical",
			description: "git clean -f permanently deletes untracked files. Combined with -d, it removes entire untracked directories. This cannot be undone.",
			suggestion:  "Use `git clean -n` (dry run) first to preview what will be deleted, or `git stash -u` to stash untracked files.",
		})
	}

	if reGitBranchDelete.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "branch_force_delete",
			severity:    "critical",
			description: "git branch -D force-deletes a branch, permanently losing any unmerged commits on that branch.",
			suggestion:  "Use `git branch -d` (safe delete) which only succeeds if the branch is fully merged.",
		})
	}

	if reGitCheckoutDiscard.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "discard_all",
			severity:    "critical",
			description: "This command discards ALL uncommitted changes in the working tree. This cannot be undone.",
			suggestion:  "Use `git stash` to save changes before discarding, or discard specific files with `git checkout -- <file>`.",
		})
	}

	if reGitStashDrop.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "stash_drop",
			severity:    "warning",
			description: "git stash drop/clear permanently removes stashed changes. If the stashed work is needed later, it cannot be recovered (except via fsck within ~30 days).",
			suggestion:  "Consider keeping stashes until you're certain they're no longer needed.",
		})
	}

	if reGitRebase.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "rebase",
			severity:    "warning",
			description: "git rebase rewrites commit history. If these commits have been pushed, collaborators will need to reset their local branches. Conflicts may require manual resolution.",
			suggestion:  "Consider `git merge` instead, which preserves history. If rebasing, ensure you're on a local (unpushed) branch.",
		})
	}

	if reGitFilterBranch.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "filter_branch",
			severity:    "critical",
			description: "git filter-branch/filter-repo rewrites entire repository history. This is extremely dangerous on shared repositories and can corrupt collaborator clones.",
			suggestion:  "Only use on local/personal repositories. Communicate with all collaborators before rewriting shared history.",
		})
	}

	if reRmRf.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "rm_rf",
			severity:    "critical",
			description: "rm -rf recursively force-deletes files without confirmation. This is extremely dangerous and can destroy entire project directories.",
			suggestion:  "Be very specific about paths. Consider moving to a temp directory instead of deleting. Never use rm -rf on root, home, or project root paths.",
		})
	}

	return found
}

// detectDestructiveInGitTool analyzes arguments from git_* built-in tools
// (git_reset, git_stash, git_revert, git_checkout) for destructive operations.
func detectDestructiveInGitTool(toolName string, args json.RawMessage) []destructivePattern {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return nil
	}

	var found []destructivePattern

	switch toolName {
	case "git_reset":
		mode, _ := m["mode"].(string)
		if mode == "hard" {
			found = append(found, destructivePattern{
				name:        "reset_hard",
				severity:    "critical",
				description: "git reset --hard permanently discards all uncommitted changes (staged and unstaged). This cannot be undone.",
				suggestion:  "Use mode 'soft' to unstage without losing work, or mode 'mixed' (default) to unstage and keep changes.",
			})
		}
	case "git_stash":
		action, _ := m["action"].(string)
		if action == "drop" || action == "clear" {
			found = append(found, destructivePattern{
				name:        "stash_drop",
				severity:    "warning",
				description: fmt.Sprintf("git stash %s permanently removes stashed changes.", action),
				suggestion:  "Consider keeping stashes until you're certain they're no longer needed.",
			})
		}
	case "git_checkout":
		// Branch switches via git_checkout tool are low-risk; not flagged.
	}

	return found
}

// checkGitDestructive is called before tool execution to check for destructive
// git operations. Returns a warning string if a destructive pattern is detected
// (and hasn't been warned yet this run), or empty string otherwise.
func (a *Agent) checkGitDestructive(toolName string, args json.RawMessage) string {
	if a.destructiveGuard == nil {
		return ""
	}

	var patterns []destructivePattern

	switch toolName {
	case "run_command", "start_command":
		// Extract the command string from arguments
		var m map[string]any
		if err := json.Unmarshal(args, &m); err == nil {
			if cmd, ok := m["command"].(string); ok {
				patterns = detectDestructiveInShellCommand(cmd)
			}
		}
	case "git_reset", "git_stash", "git_revert", "git_checkout":
		patterns = detectDestructiveInGitTool(toolName, args)
	}

	if len(patterns) == 0 {
		return ""
	}

	// Filter out already-warned patterns (once per unique pattern per run)
	var newPatterns []destructivePattern
	for _, p := range patterns {
		if !a.destructiveGuard.warned[p.name] {
			newPatterns = append(newPatterns, p)
			a.destructiveGuard.warned[p.name] = true
		}
	}

	if len(newPatterns) == 0 {
		return ""
	}

	debug.Log("git_destructive", "detected %d destructive pattern(s) in tool %s", len(newPatterns), toolName)

	// Build warning message
	var sb strings.Builder
	sb.WriteString("\n")
	hasCritical := false
	for _, p := range newPatterns {
		if p.severity == "critical" {
			hasCritical = true
		}
	}

	if hasCritical {
		sb.WriteString("DESTRUCTIVE GIT OPERATION DETECTED\n")
	} else {
		sb.WriteString("Git operation caution\n")
	}

	for _, p := range newPatterns {
		icon := "Warning:"
		if p.severity == "critical" {
			icon = "CRITICAL:"
		}
		sb.WriteString(fmt.Sprintf("\n%s [%s]\n", icon, p.name))
		sb.WriteString(fmt.Sprintf("  Risk: %s\n", p.description))
		sb.WriteString(fmt.Sprintf("  Safer alternative: %s\n", p.suggestion))
	}

	sb.WriteString("\nIf this operation is intentional and necessary, proceed. Otherwise, use a safer alternative.\n")

	return sb.String()
}
