package agent

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

var pushShapeRe = regexp.MustCompile(`(?m)^\s*\+?\s*[0-9a-f]+\.{2,3}[0-9a-f]+\s+\S+\s+->\s+\S+`)

type staleReviewState struct {
	warned map[string]bool
}

func newStaleReviewState() *staleReviewState {
	return &staleReviewState{warned: map[string]bool{}}
}

func (s *staleReviewState) reset() { s.warned = map[string]bool{} }

func ghQuery(workingDir string, args ...string) (string, bool) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = workingDir
	out, err := runGitCommandWithTimeout(cmd, 15*time.Second)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

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

	c := strings.TrimSpace(payload.Command)
	if !strings.HasPrefix(c, "git") || !strings.Contains(c, " push") {
		return ""
	}
	refUpdate := pushShapeRe.FindStringSubmatch(output)
	if refUpdate == nil {
		return ""
	}

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

	prNum, ok := ghQuery(workingDir, "pr", "list", "--head", remoteBranch, "--state", "open", "--json", "number", "--jq", ".[0].number")
	if !ok || prNum == "" {
		return ""
	}

	decision, okD := ghQuery(workingDir, "pr", "view", prNum, "--json", "latestReviews,reviewDecision", "--jq",
		".reviewDecision + \"|\" + ((.latestReviews // []) | map(.state) | join(\",\"))")
	if !okD || decision == "" {
		return ""
	}

	hasApproval := strings.Contains(decision, "APPROVED")
	overallApproved := strings.HasPrefix(decision, "APPROVED")
	if !hasApproval || overallApproved {
		return ""
	}

	a.staleReview.warned[remoteBranch] = true
	debug.Log("stale-review", "post-push review staleness on PR %s (branch %s): %q", prNum, remoteBranch, decision)
	return fmt.Sprintf(
		"[Post-push notice (#4779): this push advanced branch %s past its reviewed commits - the earlier APPROVED review on PR #%s "+
			"is now stale/dismissed and the merge WILL be refused until a reviewer re-approves. "+
			"Request re-review now (re-request the reviewer on the PR or ask in-thread) instead of discovering this at merge time.]",
		remoteBranch, prNum)
}
