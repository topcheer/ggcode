package tool

// CI/CD Pipeline Status Integration
//
// Research basis: All major AI coding agents (Claude Code, Cursor, Windsurf,
// Cline/OpenHands) support a CI feedback loop — push code, check if CI passes,
// read failure logs, and fix issues autonomously. Without this capability, the
// agent pushes code and then has no way to know whether CI succeeded or failed,
// forcing the user to manually check and relay results back.
//
// This tool uses the GitHub CLI (`gh`) to:
//   1. Check the latest workflow run status for the current branch
//   2. List recent workflow runs
//   3. Retrieve failed job logs for diagnosis
//
// Requirements: `gh` CLI must be installed and authenticated. The tool is
// read-only — it never triggers or cancels runs.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// ciStatusTimeout caps how long any gh subprocess can run.
const ciStatusTimeout = 30 * time.Second

// ciMaxLogLines caps the number of log lines returned for failed jobs.
const ciMaxLogLines = 100

// CIStatusTool implements the ci_status tool.
type CIStatusTool struct{ WorkingDir string }

func (t CIStatusTool) Name() string { return "ci_status" }

func (t CIStatusTool) Description() string {
	return "Check CI/CD pipeline status for the current branch using GitHub CLI (gh). " +
		"Supports three actions: 'status' (latest run for current branch), 'list' (recent runs), " +
		"'logs' (failed job logs for diagnosis). Requires gh CLI to be installed and authenticated."
}

func (t CIStatusTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["status", "list", "logs"],
				"description": "status: latest workflow run for current branch; list: recent workflow runs (last 10); logs: logs from the most recent failed run"
			},
			"branch": {
				"type": "string",
				"description": "Branch name (default: current branch from git)"
			},
			"path": {
				"type": "string",
				"description": "Repository path (default: current directory)"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["action", "description"]
	}`)
}

func (t CIStatusTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action string `json:"action"`
		Branch string `json:"branch"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	dir := resolveDir(args.Path, t.WorkingDir)

	// Verify gh CLI is available
	if _, err := exec.LookPath("gh"); err != nil {
		return Result{IsError: true, Content: "GitHub CLI (gh) is not installed or not in PATH. " +
			"Install it from https://cli.github.com/ to use CI status checking."}, nil
	}

	// Verify we're in a GitHub repo
	repoInfo, err := ghRepoInfo(ctx, dir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Not a GitHub repository or gh not authenticated: %v", err)}, nil
	}

	branch := args.Branch
	if branch == "" {
		branch, err = currentBranch(ctx, dir)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("Could not determine current branch: %v", err)}, nil
		}
	}

	switch args.Action {
	case "status":
		return t.getStatus(ctx, dir, repoInfo, branch)
	case "list":
		return t.listRuns(ctx, dir, repoInfo)
	case "logs":
		return t.getFailedLogs(ctx, dir, repoInfo, branch)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("Unknown action: %s. Use 'status', 'list', or 'logs'.", args.Action)}, nil
	}
}

// ghRepoInfo returns owner/repo for the current directory.
func ghRepoInfo(ctx context.Context, dir string) (string, error) {
	out, err := ghRun(ctx, dir, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
	if err != nil {
		return "", fmt.Errorf("gh repo view failed: %w", err)
	}
	repo := strings.TrimSpace(out)
	if repo == "" {
		return "", fmt.Errorf("empty repo name returned")
	}
	return repo, nil
}

func (t CIStatusTool) getStatus(ctx context.Context, dir, repo, branch string) (Result, error) {
	// Get the latest run for this branch
	out, err := ghRun(ctx, dir,
		"run", "list",
		"--branch", branch,
		"--limit", "1",
		"--json", "status,conclusion,name,headSha,createdAt,event,databaseId",
	)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Failed to query workflow runs: %v", err)}, nil
	}

	var runs []struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		Name       string `json:"name"`
		HeadSha    string `json:"headSha"`
		CreatedAt  string `json:"createdAt"`
		Event      string `json:"event"`
		DatabaseId int64  `json:"databaseId"`
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Failed to parse run data: %v", err)}, nil
	}

	if len(runs) == 0 {
		return Result{Content: fmt.Sprintf("No workflow runs found for branch %q in %s.", branch, repo)}, nil
	}

	latest := runs[0]
	var summary string
	switch {
	case latest.Status == "completed" && latest.Conclusion == "success":
		summary = fmt.Sprintf("CI PASSED for branch %q (%s)\nWorkflow: %s\nCommit: %s\nEvent: %s\nRun ID: %d",
			branch, repo, latest.Name, shortSHA(latest.HeadSha), latest.Event, latest.DatabaseId)
	case latest.Status == "completed" && latest.Conclusion == "failure":
		summary = fmt.Sprintf("CI FAILED for branch %q (%s)\nWorkflow: %s\nCommit: %s\nEvent: %s\nRun ID: %d\n\nUse action='logs' to retrieve failure details.",
			branch, repo, latest.Name, shortSHA(latest.HeadSha), latest.Event, latest.DatabaseId)
	case latest.Status == "completed" && latest.Conclusion == "cancelled":
		summary = fmt.Sprintf("CI CANCELLED for branch %q (%s)\nWorkflow: %s\nRun ID: %d",
			branch, repo, latest.Name, latest.DatabaseId)
	case latest.Status == "in_progress":
		summary = fmt.Sprintf("CI IN PROGRESS for branch %q (%s)\nWorkflow: %s\nRun ID: %d\nCheck again shortly.",
			branch, repo, latest.Name, latest.DatabaseId)
	default:
		summary = fmt.Sprintf("CI status: %s (conclusion: %q) for branch %q (%s)\nWorkflow: %s\nRun ID: %d",
			latest.Status, latest.Conclusion, branch, repo, latest.Name, latest.DatabaseId)
	}

	return Result{Content: summary}, nil
}

func (t CIStatusTool) listRuns(ctx context.Context, dir, repo string) (Result, error) {
	out, err := ghRun(ctx, dir,
		"run", "list",
		"--limit", "10",
		"--json", "status,conclusion,name,headBranch,createdAt,event,databaseId",
	)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Failed to list workflow runs: %v", err)}, nil
	}

	var runs []struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		Name       string `json:"name"`
		HeadBranch string `json:"headBranch"`
		CreatedAt  string `json:"createdAt"`
		Event      string `json:"event"`
		DatabaseId int64  `json:"databaseId"`
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Failed to parse run list: %v", err)}, nil
	}

	if len(runs) == 0 {
		return Result{Content: fmt.Sprintf("No workflow runs found in %s.", repo)}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Recent CI runs in %s:\n\n", repo))
	for _, rr := range runs {
		icon := statusIcon(rr.Status, rr.Conclusion)
		b.WriteString(fmt.Sprintf("%s [%s] %s - branch: %s, event: %s, run: %d\n",
			icon, rr.Status, rr.Name, rr.HeadBranch, rr.Event, rr.DatabaseId))
	}
	return Result{Content: b.String()}, nil
}

func (t CIStatusTool) getFailedLogs(ctx context.Context, dir, repo, branch string) (Result, error) {
	// Find the most recent failed run for this branch
	out, err := ghRun(ctx, dir,
		"run", "list",
		"--branch", branch,
		"--status", "failure",
		"--limit", "1",
		"--json", "databaseId,name",
	)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Failed to find failed runs: %v", err)}, nil
	}

	var runs []struct {
		DatabaseId int64  `json:"databaseId"`
		Name       string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) == 0 {
		return Result{IsError: true, Content: fmt.Sprintf("No failed runs found for branch %q, or parse error.", branch)}, nil
	}

	runID := runs[0].DatabaseId

	// Get failed jobs
	jobsOut, err := ghRun(ctx, dir,
		"run", "view", fmt.Sprintf("%d", runID),
		"--json", "jobs",
	)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Failed to get job details for run %d: %v", runID, err)}, nil
	}

	var runView struct {
		Jobs []struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
			Steps      []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
				Number     int    `json:"number"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(jobsOut), &runView); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Failed to parse job data: %v", err)}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Failed run %d (%s) in %s:\n\n", runID, runs[0].Name, repo))

	hasFailed := false
	for _, jb := range runView.Jobs {
		if jb.Conclusion != "failure" {
			continue
		}
		hasFailed = true
		b.WriteString(fmt.Sprintf("Job: %s (FAILED)\n", jb.Name))
		for _, step := range jb.Steps {
			if step.Conclusion == "failure" {
				b.WriteString(fmt.Sprintf("  -> Step #%d FAILED: %s\n", step.Number, step.Name))
			}
		}
		b.WriteString("\n")
	}

	if !hasFailed {
		b.WriteString("No failed jobs found in this run (may have been cancelled).\n")
	}

	// Try to get the actual log tail for the first failed job
	logOut, err := ghRun(ctx, dir, "run", "view", fmt.Sprintf("%d", runID), "--log-failed")
	if err == nil && strings.TrimSpace(logOut) != "" {
		lines := strings.Split(strings.TrimSpace(logOut), "\n")
		if len(lines) > ciMaxLogLines {
			b.WriteString(fmt.Sprintf("Last %d log lines (of %d):\n", ciMaxLogLines, len(lines)))
			b.WriteString(strings.Join(lines[len(lines)-ciMaxLogLines:], "\n"))
		} else {
			b.WriteString("Failure logs:\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}

	return Result{Content: b.String()}, nil
}

// Clone returns an independent copy of this tool.
func (t CIStatusTool) Clone() Tool {
	return &CIStatusTool{WorkingDir: t.WorkingDir}
}

// ghRun executes a gh command with timeout.
func ghRun(ctx context.Context, dir string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, ciStatusTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "gh", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		debug.Log("ci-status", "gh %s failed: %v: %s", strings.Join(args, " "), err, string(out))
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// currentBranch returns the current git branch name.
func currentBranch(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// shortSHA truncates a full SHA to a short form.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// statusIcon returns a one-char indicator for a run status.
func statusIcon(status, conclusion string) string {
	switch {
	case status == "in_progress":
		return "..."
	case status == "queued":
		return "[Q]"
	case status == "completed" && conclusion == "success":
		return "[OK]"
	case status == "completed" && conclusion == "failure":
		return "[!!]"
	case status == "completed" && conclusion == "cancelled":
		return "[X]"
	default:
		return "[?]"
	}
}
