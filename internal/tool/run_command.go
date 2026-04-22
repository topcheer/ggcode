package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
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
	return "Execute a shell command and return stdout/stderr. Has a 30-minute timeout by default."
}

func (t RunCommand) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "Shell command to execute"
			},
			"working_dir": {
				"type": "string",
				"description": "Working directory for the command (default: current directory)"
			},
			"timeout": {
				"type": "integer",
				"description": "Timeout in seconds (default: 1800)"
			},
			"description": {
				"type": "string",
				"description": "Clear, concise description of what this command does in active voice."
			}
		},
		"required": ["command"]
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

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
	defer cancel()

	cmd, _, err := util.NewShellCommandContext(timeoutCtx, args.Command)
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
		cmd, _, _ = util.NewShellCommandContext(timeoutCtx, args.Command)
		configureCommandCancellation(cmd)
		if t.WorkingDir != "" {
			cmd.Dir = t.WorkingDir
		}
		cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// GUI commands: start and return immediately.
	if isGUICommand(args.Command) {
		if err := cmd.Start(); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("failed to start GUI command: %v", err)}, nil
		}
		// Detach — don't wait for exit
		go func() { _ = cmd.Wait() }()
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
		return t.executeWithAutoBackground(timeoutCtx, cmd, args.Command, &stdout, &stderr, delay)
	}

	err = cmd.Run()

	output := stdout.String()
	errOutput := stderr.String()

	// Truncate output if too large
	if len(output) > maxOutputSize {
		output = output[:maxOutputSize] + "\n... [output truncated]"
	}
	if len(errOutput) > maxOutputSize {
		errOutput = errOutput[:maxOutputSize] + "\n... [stderr truncated]"
	}

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
		return Result{IsError: true, Content: fmt.Sprintf("%s\nCommand failed: %v", sb.String(), err)}, nil
	}

	if sb.Len() == 0 {
		return Result{Content: "Command completed with no output."}, nil
	}

	return Result{Content: sb.String()}, nil
}

// executeWithAutoBackground starts a command and races between command
// completion and the given delay. If the command finishes quickly,
// its output is returned directly. If it runs longer than the delay, it is
// automatically converted to a background job and the job ID is returned.
func (t RunCommand) executeWithAutoBackground(ctx context.Context, cmd *exec.Cmd, command string, stdout, stderr *bytes.Buffer, delay time.Duration) (Result, error) {
	if err := cmd.Start(); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to start command: %v", err)}, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		// Command completed within the delay — return output directly.
		output := stdout.String()
		errOutput := stderr.String()
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
			return Result{IsError: true, Content: fmt.Sprintf("%s\nCommand failed: %v", sb.String(), err)}, nil
		}
		if sb.Len() == 0 {
			return Result{Content: "Command completed with no output."}, nil
		}
		return Result{Content: sb.String()}, nil

	case <-time.After(delay):
		// Command is still running — auto-background it.
		if t.JobManager == nil {
			// No job manager available; wait for completion (old behavior).
			err := <-done
			output := stdout.String()
			errOutput := stderr.String()
			var sb strings.Builder
			sb.WriteString(output)
			if errOutput != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString("STDERR:\n")
				sb.WriteString(errOutput)
			}
			if err != nil {
				return Result{IsError: true, Content: fmt.Sprintf("%s\nCommand failed: %v", sb.String(), err)}, nil
			}
			return Result{Content: sb.String()}, nil
		}

		// Migrate the running process into a background job.
		jobID := t.JobManager.AutoBackground(cmd, command, stdout.String(), stderr.String())
		return Result{Content: fmt.Sprintf(
			"Command is still running after %v. Automatically moved to background (job %s).\nUse `read_command_output` to check progress or `stop_command` to stop it.",
			delay, jobID,
		)}, nil
	}
}
