package agent

import (
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// mainTreeBaselineGuard (#4803 / S-35): while the shared main tree sits on a
// fix branch, every seat running build/test/verify commands there computes
// results against the WRONG baseline for up to the branch's whole lifetime
// (80+ min in the incident). This guard appends a non-blocking advisory when
// a build/test-shaped run_command executes in a git repo whose current
// branch is not the trunk, so the model (and the log) see that the numbers
// describe a branch baseline, not main. Advisory-only by design: skipping or
// blocking would break the legitimate fix-branch verification flows the
// branches exist for.
type mainTreeBaselineGuard struct {
	mu     sync.Mutex
	cached map[string]mainTreeBranchCache // workDir -> branch ("" = unknown/not a repo)
}

type mainTreeBranchCache struct {
	branch string
	at     time.Time
}

var baselineGuardCacheTTL = 30 * time.Second

// trunkBranches lists branch names treated as "the baseline". A repo's
// default branch is usually main or master; anything else means validation
// numbers describe a non-trunk baseline.
var trunkBranches = map[string]bool{"main": true, "master": true}

// currentBranch resolves (with a short-lived cache) the current git branch
// of dir, or "" when dir is not a git repository.
func (g *mainTreeBaselineGuard) currentBranch(dir string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.cached[dir]; ok && time.Since(c.at) < baselineGuardCacheTTL {
		return c.branch
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		// Not a repo (or git missing): no advisory is meaningful.
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if g.cached == nil {
		g.cached = make(map[string]mainTreeBranchCache)
	}
	g.cached[dir] = mainTreeBranchCache{branch: branch, at: time.Now()}
	return branch
}

// advisoryFor returns the baseline advisory for a run_command invocation, or
// "" when the command is not build/test-shaped, the working dir is not a git
// repo, or the current branch is the trunk. The check is cheap (one cached
// git call) and only fires for detected build/test commands.
func (a *Agent) advisoryFor(name string, args []byte, workDir string) string {
	if name != "run_command" {
		return ""
	}
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil || parsed.Command == "" {
		return ""
	}
	if isBuild, _, _ := detectBuildTestCommand(parsed.Command); !isBuild {
		return ""
	}
	if workDir == "" {
		workDir = "."
	}
	branch := a.baselineGuard.currentBranch(workDir)
	if branch == "" || trunkBranches[branch] {
		return ""
	}
	return "\n[baseline] note: the working tree is on branch '" + branch +
		"', not the trunk - build/test results describe THIS branch's baseline. " +
		"Do not report them as main-line verification (e.g. in PR acceptance)."
}
