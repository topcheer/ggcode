package agent

// Post-Push Review-Staleness Warning (#4779 / S-07)
//
// Branch protection dismisses or invalidates reviews when new commits land
// on a review-gated branch, but nothing in the toolchain surfaces that
// until the merge is REFUSED. The resolve-conflicts-then-push flow hit this
// on #3553: the coordinator pushed the conflict-resolution commit, the
// earlier APPROVED silently went stale, and the failure only appeared at
// merge time - exactly when re-approval costs another round trip.
//
// Per the issue's suggested direction ("after a push to a review-gated
// branch, query review staleness and warn inline"): this gate inspects
// run_command results right after execution; when the command looks like a
// successful `git push` to a remote branch, it queries the PR state via gh
// (open PR on that head branch, latest review decision) and returns an
// inline warning when an earlier approval no longer applies.
//
// Advisory and non-blocking, same contract as draftpr_hint_gate. Zero LLM
// cost: one output-shape check plus at most two gh calls, only on
// push-shaped commands.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// pushShapeRe recognizes the ref-update receipt line git prints on a
// successful push. Fast-forward uses "  1a2b3c4..5d6e7f8  br -> origin/br";
// force-update (#4876) uses " + 1a2b3c4...5d6e7f8  br -> origin/br (forced
// update)" - leading '+', THREE dots, optional trailing annotation. The old
// two-dot-only pattern missed the force form entirely: the staleness gate
// stayed silent exactly where stale-review risk is highest.
var pushShapeRe = regexp.MustCompile(`(?m)^\s*\+?\s*[0-9a-f]+\.{2,3}[0-9a-f]+\s+\S+\s+->\s+\S+`)

// staleReviewState caps the warning at once per push target per run - a
// conflict-resolution loop may push several times; nagging after each adds
// noise without new information (re-approval is already known to be needed).
type staleReviewState struct {
	warned map[string]bool
}

func newStaleReviewState() *staleReviewState {
	return &staleReviewState{warned: map[string]bool{}}
}

func (s *staleReviewState) reset() { s.warned = map[string]bool{} }

// ghQuery runs a gh CLI query with a short timeout; returns ("", false) on
// any failure (gh missing, network, auth) - the gate must never block work
// on tooling it does not control.
func ghQuery(workingDir string, args ...string) (string, bool) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = workingDir
	out, err := runGitCommandWithTimeout(cmd, 15*time.Second)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// checkPushReviewStaleness returns a non-empty warning when the completed
// run_command was a successful push to a branch whose PR review state just
// went stale. Returns "" for everything else. args is the tool-call's raw
// JSON arguments; the command string is parsed here so the wiring stays a
// one-liner.
func (a *Agent) checkPushReviewStaleness(toolName string, args json.RawMessage, output string) string {
	if a.staleReview == nil {
		return ""
	}
	if toolName != "run_command" && toolName != "start_command" {
		return ""
	}
	var payload struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(args, &payload) != nil || payload.Command == "" {
		return ""
	}
	// Cheap shape gate BEFORE any subprocess: only git push commands with a
	// successful ref-update receipt trigger the gh queries.
	c := strings.TrimSpace(payload.Command)
	if !strings.HasPrefix(c, "git") || !strings.Contains(c, " push") {
		return ""
	}
	refUpdate := pushShapeRe.FindStringSubmatch(output)
	if refUpdate == nil {
		return "" // failed or no-op push
	}
	// Extract the pushed remote branch from the receipt tail ("-> origin/br").
	line := refUpdate[0]
	remoteRef := strings.TrimSpace(line[strings.LastIndex(line, "->")+2:])
	remoteBranch := strings.TrimPrefix(strings.TrimPrefix(remoteRef, "origin/"), "refs/heads/")

	workingDir := a.WorkingDir()
	if workingDir == "" || remoteBranch == "" {
		return ""
	}
	if a.staleReview.warned[remoteBranch] {
		return ""
	}

	// Open PR on this head branch? (empty = no PR, nothing to go stale.)
	prNum, ok := ghQuery(workingDir, "pr", "list", "--head", remoteBranch, "--state", "open", "--json", "number", "--jq", ".[0].number")
	if !ok || prNum == "" {
		return ""
	}

	// Overall decision plus the latest per-reviewer review states in one
	// view query ("DECISION|state,state").
	decision, okD := ghQuery(workingDir, "pr", "view", prNum, "--json", "latestReviews,reviewDecision", "--jq",
		".reviewDecision + \"|\" + ((.latestReviews // []) | map(.state) | join(\",\"))")
	if !okD || decision == "" {
		return ""
	}
	// An approval recorded in latestReviews while the OVERALL decision is
	// not APPROVED means the approval was dismissed or is stale relative to
	// the new head - the merge will be refused. A fresh APPROVED decision
	// exactly matches the head and merges fine.
	hasApproval := strings.Contains(decision, "APPROVED")
	overallApproved := strings.HasPrefix(decision, "APPROVED")
	if !hasApproval || overallApproved {
		return "" // never approved, or approval still effective
	}

	a.staleReview.warned[remoteBranch] = true
	debug.Log("stale-review", "post-push review staleness on PR %s (branch %s): %q", prNum, remoteBranch, decision)
	return fmt.Sprintf(
		"[Post-push notice (#4779): this push advanced branch %s past its reviewed commits - the earlier APPROVED review on PR #%s "+
			"is now stale/dismissed and the merge WILL be refused until a reviewer re-approves. "+
			"Request re-review now (re-request the reviewer on the PR or ask in-thread) instead of discovering this at merge time.]",
		remoteBranch, prNum)
}
