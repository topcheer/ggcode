package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	maxOutputSize = 100 * 1024 // 100KB
)

// RunCommand implements the run_command tool for executing shell commands.
type RunCommand struct {
	// WorkingDir is the fixed working directory set by the agent.
	// LLM-provided working_dir is ignored to prevent sandbox escape.
	WorkingDir string
	// JobManager is used to auto-background long-running commands.
	JobManager *CommandJobManager
	// Policy provides the current permission mode. When set and the mode
	// is Bypass or Autopilot, "Ask" gate results are automatically downgraded
	// to Allow (with a warning log) instead of blocking execution.
	Policy permission.PermissionPolicy
	// OutputTee, if non-nil, receives a copy of stdout/stderr in real time.
	// Used by the TUI to mirror command output to a tmux pane.
	OutputTee io.Writer
	// OnPreExec, if non-nil, is called just before the command starts.
	OnPreExec func(command, description string)
	// OnPostExec, if non-nil, is called after the command finishes.
	OnPostExec func(exitCode int, err error)
}

// autoBackgroundDelay is how long a dev-server-like command runs before
// being automatically moved to a background job.
const autoBackgroundDelay = 15 * time.Second

// stalledCommandDelay is how long a normal (non-dev-server) command runs
// before being automatically moved to a background job. This prevents
// unexpectedly slow commands from blocking the agent loop for the full
// 30-minute timeout. The model can then decide to wait_command, check
// output, or continue with other work.
const stalledCommandDelay = 120 * time.Second

// guiCommands are commands that launch GUI applications and should return
// immediately rather than waiting for process exit.
var guiCommands = []string{
	"open",     // macOS: open file/app
	"xdg-open", // Linux: open file/app
	"start",    // Windows: start command (via bash)
	"code",     // VS Code
	"cursor",   // Cursor editor
	"windsurf", // Windsurf editor
}

// devServerPrefixes are command prefixes that typically start long-running
// dev servers. These should auto-background after autoBackgroundDelay.
var devServerPrefixes = []string{
	"npm start",
	"npm run dev",
	"npm run serve",
	"npm run start",
	"yarn dev",
	"yarn start",
	"yarn serve",
	"pnpm dev",
	"pnpm start",
	"pnpm serve",
	"bun dev",
	"bun start",
	"npx serve",
	"npx http-server",
	"python -m http.server",
	"python3 -m http.server",
	"ruby -run -e httpd",
	"go run",
	"cargo run",
	"make watch",
	"docker compose up",
	"docker-compose up",
}

// isGUICommand returns true if the command launches a GUI application
// and should return immediately after starting.
func isGUICommand(cmd string) bool {
	firstWord := firstShellWord(cmd)
	for _, gc := range guiCommands {
		if firstWord == gc {
			return true
		}
	}
	return false
}

// isDevServerCommand returns true if the command likely starts a dev server
// or other long-running foreground process.
func isDevServerCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	for _, prefix := range devServerPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// firstShellWord extracts the first word of a shell command,
// handling quoted strings.
func firstShellWord(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	// Handle quoted first word
	if cmd[0] == '"' || cmd[0] == '\'' {
		quote := cmd[0]
		for i := 1; i < len(cmd); i++ {
			if cmd[i] == quote {
				return cmd[1:i]
			}
		}
		return cmd[1:]
	}
	// Unquoted: split on whitespace
	for i, c := range cmd {
		if c == ' ' || c == '\t' {
			return cmd[:i]
		}
	}
	return cmd
}

func (t RunCommand) Name() string { return "run_command" }

func (t RunCommand) Description() string {
	return "Execute a shell command. Use for quick one-shot execution such as builds, tests, git commands, and focused repro steps. For long-running, streaming, or interactive commands, prefer start_command so you can poll output or provide stdin. Long commands may still be automatically moved to a background job; use read_command_output, wait_command, or stop_command with the returned job ID. Has a 30-minute timeout by default."
}

func (t RunCommand) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "Shell command to execute. Use for quick one-shot commands; prefer start_command for long-running, streaming, or interactive commands. IMPORTANT: Start the command with a '# ' comment line describing its purpose (e.g. '# Run tests' or '# Install dependencies'). This comment is shown as the activity label in the UI."
		},
		"timeout": {
			"type": "integer",
			"description": "Timeout in seconds (default: 1800)"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"command",
		"description"
	]
}`)
}

// isBypassMode returns true when the permission policy allows
// automatic execution of Ask-level commands (Bypass or Autopilot).
func (t RunCommand) isBypassMode() bool {
	if t.Policy == nil {
		return false
	}
	m := t.Policy.Mode()
	return m == permission.BypassMode || m == permission.AutopilotMode
}

func (t RunCommand) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Command     string `json:"command"`
		WorkingDir  string `json:"working_dir"`
		Timeout     int    `json:"timeout"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Description != "" {
		debug.Log("run_command", "description: %s", args.Description)
	}

	// === Safety gate — Allow/Ask/Block (runs regardless of autopilot) ===
	gate := NewCommandGate()
	gateResult := gate.Check(args.Command)
	if gateResult.IsBlocked() {
		debug.Log("run_command", "BLOCKED: %s", gateResult.Reason)
		return Result{IsError: true, Content: gateResult.Reason}, nil
	}
	if gateResult.NeedsConfirmation() {
		// In Bypass/Autopilot mode, Ask is automatically downgraded to Allow.
		// These modes assume the user trusts the agent — the command is
		// still logged as a warning for audit purposes.
		if t.isBypassMode() {
			debug.Log("run_command", "ASK→ALLOW (bypass mode): %s", gateResult.Reason)
		} else {
			debug.Log("run_command", "ASK: %s", gateResult.Reason)
			// Return as error with special prefix — the caller (agent loop)
			// will interpret this as "needs user permission" and prompt.
			return Result{IsError: true, Content: "⚠️ " + gateResult.Reason}, nil
		}
	}
	if len(gateResult.Warnings) > 0 {
		for _, w := range gateResult.Warnings {
			debug.Log("run_command", "WARNING: %s", w)
		}
	}
	// Use cleaned command if modifications were made
	if gateResult.CleanedCmd != "" && gateResult.CleanedCmd != args.Command {
		debug.Log("run_command", "cleaned command: %s → %s", args.Command, gateResult.CleanedCmd)
		args.Command = gateResult.CleanedCmd
	}

	if args.Timeout <= 0 {
		args.Timeout = int(defaultCommandTimeout / time.Second)
	}

	var cmdCtx context.Context
	var cancel context.CancelFunc
	if t.JobManager != nil {
		// Managed background jobs outlive this tool call, so their context must
		// not derive from the request context or be deferred here.
		cmdCtx, cancel = context.WithTimeout(context.Background(), time.Duration(args.Timeout)*time.Second)
	} else {
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
		defer cancel()
	}

	cmd, _, err := util.NewShellCommandContext(cmdCtx, args.Command)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to resolve shell: %v", err)}, nil
	}
	configureCommandCancellation(cmd)
	// Use the fixed WorkingDir from agent, ignore LLM-provided working_dir
	if t.WorkingDir != "" {
		cmd.Dir = t.WorkingDir
	}

	// Inject GIT_PAGER=cat for git commands to prevent pager hangs
	if isGitCommand(args.Command) {
		cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	}
	// Inject Co-Authored-By trailer for git commit commands
	if isGitCommitCommand(args.Command) {
		args.Command = injectCoAuthorTrailer(args.Command)
		cmd, _, _ = util.NewShellCommandContext(cmdCtx, args.Command)
		configureCommandCancellation(cmd)
		if t.WorkingDir != "" {
			cmd.Dir = t.WorkingDir
		}
		cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	}

	if t.OnPreExec != nil {
		t.OnPreExec(args.Command, args.Description)
	}

	var stdout, stderr bytes.Buffer
	if t.OutputTee != nil {
		cmd.Stdout = io.MultiWriter(&stdout, t.OutputTee)
		cmd.Stderr = io.MultiWriter(&stderr, t.OutputTee)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	// GUI commands: start and return immediately.
	if isGUICommand(args.Command) {
		if err := cmd.Start(); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("failed to start GUI command: %v", err)}, nil
		}
		// Detach — don't wait for exit
		safego.Go("tool.runCommand.guiWait", func() { _ = cmd.Wait() })
		return Result{Content: fmt.Sprintf("GUI application launched (pid %d).", cmd.Process.Pid)}, nil
	}

	// For non-GUI commands, start the process and race between
	// completion and auto-background delay. This prevents slow commands
	// from blocking the agent loop.
	// - Dev server commands: 15s threshold (known long-running)
	// - Other commands: 120s threshold (stalled detection)
	if t.JobManager != nil {
		delay := stalledCommandDelay
		if isDevServerCommand(args.Command) {
			delay = autoBackgroundDelay
		}
		return t.executeWithAutoBackground(cmdCtx, cancel, cmd, args.Command, time.Duration(args.Timeout)*time.Second, delay)
	}

	err = cmd.Run()

	output := stdout.String()
	errOutput := stderr.String()

	// Truncate output if too large — keep both head and tail.
	// For most commands (tests, builds, lints), the important info is at the
	// end (error messages, test results). Keeping only the head would lose it.
	output = truncateMiddle(output, maxOutputSize, "output")
	errOutput = truncateMiddle(errOutput, maxOutputSize, "stderr")

	var sb strings.Builder
	if output != "" {
		sb.WriteString(output)
	}
	if errOutput != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("STDERR:\n")
		sb.WriteString(errOutput)
	}

	if err != nil {
		exitCode := -1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		if t.OnPostExec != nil {
			t.OnPostExec(exitCode, err)
		}
		return Result{IsError: true, Content: fmt.Sprintf("%s\nCommand failed: %v", sb.String(), err)}, nil
	}

	if t.OnPostExec != nil {
		t.OnPostExec(0, nil)
	}

	if sb.Len() == 0 {
		return Result{Content: "Command completed with no output."}, nil
	}

	return Result{Content: sb.String()}, nil
}

// truncateMiddle keeps the first 40% and last 50% of output, inserting a
// "[... N lines omitted ...]" marker in between. This ensures the agent sees
// both the beginning (context/setup) and the end (errors, results, exit status)
// of long outputs like test runs, build logs, and linter output.
//
// Truncation is line-aware: head and tail boundaries are snapped to the
// nearest newline so the output doesn't contain partial lines. This makes
// the truncated output much easier for the agent to parse.
func truncateMiddle(s string, maxLen int, label string) string {
	if len(s) <= maxLen {
		return s
	}
	headSize := maxLen * 2 / 5 // 40% of budget for the head
	tailSize := maxLen / 2     // 50% of budget for the tail

	// Snap head to the end of the current line (don't cut mid-line).
	// But only snap forward by at most maxLen/10 to avoid swallowing
	// huge single-line content.
	headEnd := headSize
	if idx := strings.Index(s[headSize:], "\n"); idx >= 0 && idx <= headSize/2 {
		headEnd = headSize + idx
	}

	// Snap tail to the start of the next line (don't start mid-line).
	tailStart := len(s) - tailSize
	if idx := strings.Index(s[tailStart:], "\n"); idx >= 0 && idx <= tailSize/2 {
		tailStart = tailStart + idx + 1
	}

	// If snapping causes overlap, fall back to raw byte truncation
	if headEnd >= tailStart {
		headEnd = headSize
		tailStart = len(s) - tailSize
	}

	head := s[:headEnd]
	tail := s[tailStart:]

	// Count omitted lines for a more useful message
	omittedText := s[headEnd:tailStart]
	omittedLines := strings.Count(omittedText, "\n")

	return head + fmt.Sprintf("\n... [%d lines omitted — %s truncated, showing tail] ...\n", omittedLines, label) + tail
}

// executeWithAutoBackground starts a command as a managed job and waits up to
// the given delay. If the command finishes quickly, its output is returned
// directly. If it runs longer than the delay, the already-managed job ID is
// returned. The job manager owns the process and performs the only Wait call.
func (t RunCommand) executeWithAutoBackground(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, command string, timeout time.Duration, delay time.Duration) (Result, error) {
	if t.JobManager == nil {
		return Result{IsError: true, Content: "command job manager not available"}, nil
	}
	job, snapshot, err := t.JobManager.StartExisting(ctx, cmd, command, timeout, cancel)
	if err != nil {
		cancel()
		return Result{IsError: true, Content: err.Error()}, nil
	}
	if snapshot != nil && snapshot.Status == CommandJobFailed {
		t.JobManager.forget(snapshot.ID)
		return Result{IsError: true, Content: snapshot.ErrText}, nil
	}
	if job == nil || snapshot == nil {
		cancel()
		return Result{IsError: true, Content: "failed to start command job"}, nil
	}

	if err := waitForCommandJob(context.Background(), job, delay); err != nil {
		if t.OnPostExec != nil {
			t.OnPostExec(-1, err)
		}
		return Result{IsError: true, Content: err.Error()}, nil
	}
	snap := t.JobManager.snapshot(job)
	snapshot = &snap
	if snapshot.Status == CommandJobRunning {
		// Command is still running in background — pane footer will be
		// written when the user checks the job output or stops it.
		return Result{Content: fmt.Sprintf(
			"Command is still running after %v. Automatically moved to background (job %s).\nUse `read_command_output` to check progress or `stop_command` to stop it.",
			delay, snapshot.ID,
		)}, nil
	}

	content := commandSnapshotOutput(*snapshot)
	if snapshot.Status == CommandJobFailed || snapshot.Status == CommandJobCancelled || snapshot.Status == CommandJobTimedOut {
		t.JobManager.forget(snapshot.ID)
		if t.OnPostExec != nil {
			t.OnPostExec(-1, fmt.Errorf("status: %s", snapshot.Status))
		}
		return Result{IsError: true, Content: content}, nil
	}
	t.JobManager.forget(snapshot.ID)
	if t.OnPostExec != nil {
		t.OnPostExec(0, nil)
	}
	return Result{Content: content}, nil
}

func commandSnapshotOutput(snapshot CommandJobSnapshot) string {
	var sb strings.Builder
	for _, line := range snapshot.Lines {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(line)
	}
	if snapshot.ErrText != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(snapshot.ErrText)
	}
	if sb.Len() == 0 {
		return "Command completed with no output."
	}
	return sb.String()
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t RunCommand) Clone() Tool {
	return &RunCommand{
		WorkingDir: t.WorkingDir,
		Policy:     t.Policy,
		OutputTee:  t.OutputTee,
		OnPreExec:  t.OnPreExec,
		OnPostExec: t.OnPostExec,
	}
}
