package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// GitBisect implements the git_bisect tool: agent-friendly regression
// bisection. Bisection is inherently stateful (git checks out a candidate
// commit on every good/bad step, and leaves the repo in a detached state
// until reset), which makes raw `git bisect` via run_command error-prone for
// agents: they lose track of which commit is checked out, forget to reset,
// or get stuck in a half-finished session. This tool wraps the full state
// machine, reports the current checkout after every step, detects the
// "first bad commit" verdict, and always offers an explicit reset path.
type GitBisect struct{ WorkingDir string }

func (t GitBisect) Name() string { return "git_bisect" }

func (t GitBisect) Description() string {
	return "Find the commit that introduced a bug via git bisection. Actions: 'start' (begin with known bad + good refs), 'good'/'bad'/'skip' (classify the currently checked-out commit after you verify it), 'status' (bisection log + current state), 'run' (scripted bisection with a shell command that exits 0=good / 1-124=bad), 'reset' (end bisection and restore HEAD). After each step the current checkout is shown — verify it, then classify it. Always call 'reset' when finished."
}

func (t GitBisect) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Repository path (default: current directory)"
			},
			"action": {
				"type": "string",
				"description": "Bisection action",
				"enum": ["start", "good", "bad", "skip", "status", "run", "reset"]
			},
			"bad": {
				"type": "string",
				"description": "Known-bad ref for 'start' (default: HEAD)"
			},
			"good": {
				"type": "string",
				"description": "Known-good ref: required for 'start'; optional commit for 'good'"
			},
			"commit": {
				"type": "string",
				"description": "Optional commit ref for 'bad'/'skip' (default: current checkout)"
			},
			"command": {
				"type": "string",
				"description": "Shell command for 'run' (exit 0=good, 1-124=bad, 125=skip)"
			},
			"timeout_seconds": {
				"type": "integer",
				"description": "Timeout for 'run' command (default 300, max 1800)"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["description"]
	}`)
}

// maxRunOutput caps the scripted-run output returned to the agent.
const maxRunOutput = 16 * 1024

// gitBisectArgs is the decoded input for the git_bisect tool.
type gitBisectArgs struct {
	Path           string `json:"path"`
	Action         string `json:"action"`
	Bad            string `json:"bad"`
	Good           string `json:"good"`
	Commit         string `json:"commit"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (t GitBisect) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args gitBisectArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := args.Action
	if action == "" {
		action = "status"
	}
	dir := resolveDir(args.Path, t.WorkingDir)

	// Flag-family guard (same convention as git_tag #2133/#2139): user-supplied
	// refs go into the git command line as plain arguments, so a leading dash
	// would be parsed as a git option. Refuse uniformly before exec.
	for refName, ref := range map[string]string{"bad": args.Bad, "good": args.Good, "commit": args.Commit} {
		if strings.HasPrefix(ref, "-") {
			return Result{IsError: true, Content: fmt.Sprintf("invalid %s ref %q: leading dash (refusing option injection)", refName, ref)}, nil
		}
	}

	switch action {
	case "start":
		return t.bisectStart(ctx, dir, args)
	case "good", "bad", "skip":
		return t.bisectStep(ctx, dir, action, args.Commit)
	case "status":
		return t.bisectStatus(ctx, dir)
	case "run":
		return t.bisectRun(ctx, dir, args)
	case "reset":
		return t.bisectReset(ctx, dir)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported action %q: must be start, good, bad, skip, status, run, or reset", action)}, nil
	}
}

func (t GitBisect) bisectStart(ctx context.Context, dir string, args gitBisectArgs) (Result, error) {
	if args.Good == "" {
		return Result{IsError: true, Content: "good is required for start action: provide a ref known to predate the bug (e.g. a tag, or 'HEAD~10'). Find candidates with git_log first."}, nil
	}
	bad := args.Bad
	if bad == "" {
		bad = "HEAD"
	}

	gitArgs := []string{"bisect", "start", bad, args.Good}
	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git bisect start failed: %v\n%s\nIf a bisection is already in progress, call action=reset first.", err, out)}, nil
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(string(out)))
	// Warn early about a dirty tree: modified tracked files block the
	// candidate checkouts git performs on every step.
	statusCmd := gitCommand(ctx, "status", "--porcelain")
	statusCmd.Dir = dir
	if statusOut, serr := statusCmd.CombinedOutput(); serr == nil && strings.TrimSpace(string(statusOut)) != "" {
		b.WriteString("\n\nNote: working tree has uncommitted changes; they follow the bisection checkouts. Commit or stash them if a checkout fails.")
	}
	b.WriteString("\n\n" + t.checkoutLine(ctx, dir))
	b.WriteString("\nNext: verify this commit (build/tests), then call action=good or action=bad. Use action=skip if it does not build.")
	return Result{Content: b.String()}, nil
}

func (t GitBisect) bisectStep(ctx context.Context, dir, action, commit string) (Result, error) {
	gitArgs := []string{"bisect", action}
	if commit != "" {
		gitArgs = append(gitArgs, commit)
	}
	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if isNoBisectionText(text) {
			return Result{IsError: true, Content: "No bisection in progress. Call action=start with a known bad ref and a known good ref."}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("git bisect %s failed: %v\n%s", action, err, text)}, nil
	}

	var b strings.Builder
	b.WriteString(text)
	if bisectComplete(text) {
		b.WriteString("\n\nBisection complete. Call action=reset to return to your original HEAD.")
	} else {
		b.WriteString("\n\n" + t.checkoutLine(ctx, dir))
		b.WriteString("\nNext: verify this commit, then call action=good or action=bad (action=skip if it does not build).")
	}
	return Result{Content: b.String()}, nil
}

func (t GitBisect) bisectStatus(ctx context.Context, dir string) (Result, error) {
	cmd := gitCommand(ctx, "bisect", "log")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(text, "not bisecting") || strings.Contains(text, "We are not bisecting") || text == "" {
			return Result{Content: "No bisection in progress. Start one with action=start (needs a known bad ref and a known good ref)."}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("git bisect log failed: %v\n%s", err, text)}, nil
	}
	return Result{Content: text + "\n\n" + t.checkoutLine(ctx, dir)}, nil
}

func (t GitBisect) bisectRun(ctx context.Context, dir string, args gitBisectArgs) (Result, error) {
	if strings.TrimSpace(args.Command) == "" {
		return Result{IsError: true, Content: "command is required for run action: a shell command that exits 0 (good), 1-124 (bad), or 125 (skip)"}, nil
	}
	if !inBisection(ctx, dir) {
		return Result{IsError: true, Content: "No bisection in progress. Call action=start (needs good ref) before action=run."}, nil
	}

	timeout := time.Duration(args.TimeoutSeconds) * time.Second
	if args.TimeoutSeconds <= 0 {
		timeout = 300 * time.Second
	}
	if timeout > 1800*time.Second {
		timeout = 1800 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	gitArgs := []string{"bisect", "run"}
	if runtime.GOOS == "windows" {
		gitArgs = append(gitArgs, "cmd", "/c", args.Command)
	} else {
		gitArgs = append(gitArgs, "sh", "-c", args.Command)
	}
	cmd := gitCommand(runCtx, gitArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := string(out)
	if len(text) > maxRunOutput {
		text = truncateUTF8Safe(text, maxRunOutput) + "\n... [output truncated]"
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(text))
	if err != nil {
		b.WriteString(fmt.Sprintf("\n\ngit bisect run exited: %v", err))
	}
	if bisectComplete(text) {
		b.WriteString("\n\nBisection complete. Call action=reset to return to your original HEAD.")
	}
	return Result{Content: b.String()}, nil
}

func (t GitBisect) bisectReset(ctx context.Context, dir string) (Result, error) {
	cmd := gitCommand(ctx, "bisect", "reset")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(text, "not bisecting") || strings.Contains(text, "We are not bisecting") {
			return Result{Content: "No bisection in progress; nothing to reset."}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("git bisect reset failed: %v\n%s", err, text)}, nil
	}
	return Result{Content: text + "\nBisection ended, original HEAD restored."}, nil
}

// checkoutLine describes the commit git currently has checked out, so the
// agent never loses track of where it is mid-bisection.
func (t GitBisect) checkoutLine(ctx context.Context, dir string) string {
	cmd := gitCommand(ctx, "log", "-1", "--oneline")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "Current checkout: unknown (git log failed)"
	}
	return "Current checkout: " + strings.TrimSpace(string(out))
}

// bisectComplete reports whether git output contains the final bisection
// verdict. Git historically printed "<hash> is the first bad commit"; newer
// versions quote the classification: "is the first 'bad' commit". Scripted
// runs may instead end with "bisect found first 'bad' commit".
func bisectComplete(text string) bool {
	return strings.Contains(text, "is the first") || strings.Contains(text, "bisect found first")
}

// isNoBisectionText matches every git wording for "no bisection session":
// "We are not bisecting." and 'You need to start by "git bisect start"'.
func isNoBisectionText(text string) bool {
	return strings.Contains(text, "not bisecting") || strings.Contains(text, "You need to start")
}
func inBisection(ctx context.Context, dir string) bool {
	cmd := gitCommand(ctx, "bisect", "log")
	cmd.Dir = dir
	return cmd.Run() == nil
}
