package agent

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// #5212: raw `git branch -D` via run_command bypassed every tool-seam guard
// (exit_worktree remove has a rev-list probe; the detector only warns AFTER
// the branch is already gone), so a completed unpushed fix branch was
// silently deleted. This pre-flight probe resolves the branch names in a
// force-delete command, counts commits not reachable from any remote, and
// HARD-BLOCKS before execution while they exist - echoing the exit_worktree
// semantics (block + name SHAs + explicit-intent escape).

// allowUnpushedDeleteEnv is the explicit-intent escape: setting it to 1 in
// the same command (env prefix or export) confirms the deletion is
// intentional, mirroring exit_worktree(remove, discard_changes=true).
const allowUnpushedDeleteEnv = "GGCODE_ALLOW_UNPUSHED_DELETE=1"

var (
	reBranchSegment   = regexp.MustCompile(`\bgit\s+branch\s+([^\n;|&]*)`)
	reBranchDelFlag   = regexp.MustCompile(`^-D$|^-df$|^-fd$`)
	reBranchDelLongD  = regexp.MustCompile(`^--delete$`)
	reBranchDelLongF  = regexp.MustCompile(`^--force$`)
	probeMaxBranches  = 4
	probeCmdTimeout   = 3 * time.Second
	probeMaxSHAsNamed = 5
)

// extractForceDeletedBranches returns the branch names targeted by a force
// delete (-D / -df / -fd / --delete --force) in the command. Safe deletes
// (-d / --delete without --force) return nothing: git already refuses those
// on unmerged branches.
func extractForceDeletedBranches(cmd string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range reBranchSegment.FindAllStringSubmatch(cmd, -1) {
		fields := strings.Fields(m[1])
		force := false
		hasLongDelete, hasLongForce := false, false
		for _, f := range fields {
			if strings.HasPrefix(f, "-") {
				if reBranchDelFlag.MatchString(f) {
					force = true
				}
				if reBranchDelLongD.MatchString(f) {
					hasLongDelete = true
				}
				if reBranchDelLongF.MatchString(f) {
					hasLongForce = true
				}
			}
		}
		if hasLongDelete && hasLongForce {
			force = true
		}
		if !force {
			continue
		}
		for _, f := range fields {
			if f == "" || strings.HasPrefix(f, "-") {
				continue
			}
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	if len(out) > probeMaxBranches {
		out = out[:probeMaxBranches]
	}
	return out
}

// gitProbe runs a read-only git probe in workDir. Any failure returns a
// zero value: the probe is fail-safe and never blocks on its own errors
// (the post-exec advisory detector still covers those cases).
func gitProbe(workDir string, args ...string) string {
	if workDir == "" {
		workDir = "."
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", workDir}, args...)...).
		CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// branchUnpushedState returns (count, shaList) of commits on branch that no
// remote ref reaches. Empty shaList with count 0 means fully pushed.
func branchUnpushedState(workDir, branch string) (int, string) {
	countStr := gitProbe(workDir, "rev-list", "--count", branch, "--not", "--remotes")
	if countStr == "" {
		return 0, ""
	}
	count := 0
	if _, err := fmt.Sscanf(countStr, "%d", &count); err != nil || count <= 0 {
		return 0, ""
	}
	shas := gitProbe(workDir, "rev-list", branch, "--not", "--remotes",
		fmt.Sprintf("--max-count=%d", probeMaxSHAsNamed))
	return count, shas
}

// branchDeleteGuardBlock returns a hard-block message when the command
// force-deletes branches that carry unpushed commits. "" means allowed.
func branchDeleteGuardBlock(workDir, command string) string {
	if strings.Contains(command, allowUnpushedDeleteEnv) {
		return ""
	}
	branches := extractForceDeletedBranches(command)
	if len(branches) == 0 {
		return ""
	}
	var offenders []string
	for _, br := range branches {
		count, shas := branchUnpushedState(workDir, br)
		if count > 0 {
			offenders = append(offenders, fmt.Sprintf("- %s: %d unpushed commit(s)%s",
				br, count, formatProbeSHAs(shas)))
		}
	}
	if len(offenders) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"[branch-delete-guard] BLOCKED (pre-execution, #5212): force-deleting %s. "+
			"These branches carry commits not on ANY remote:\n%s\n"+
			"Silent deletion is the failure mode this guard exists for. Options: "+
			"(1) push first (git push origin <branch>); (2) merge/rebase so the commits land on a remote-reachable branch; "+
			"(3) if the work is INTENTIONALLY abandoned, re-run the exact command prefixed with %s to confirm you have read the SHAs above. "+
			"Recovery note: deleted tips remain reachable via reflog for ~90 days (git reflog show <branch>).",
		quoteBranches(branches),
		strings.Join(offenders, "\n"),
		allowUnpushedDeleteEnv)
}

func formatProbeSHAs(shas string) string {
	if shas == "" {
		return ""
	}
	lines := strings.Split(shas, "\n")
	if len(lines) > probeMaxSHAsNamed {
		lines = lines[:probeMaxSHAsNamed]
	}
	return ": " + strings.Join(lines, " ")
}

func quoteBranches(branches []string) string {
	quoted := make([]string, len(branches))
	for i, b := range branches {
		quoted[i] = "'" + b + "'"
	}
	return strings.Join(quoted, ", ")
}
