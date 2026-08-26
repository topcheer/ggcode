package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
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
	return "Execute a shell command for quick one-shot execution: builds, tests, git commands, focused repro steps. For long-running, streaming, or interactive commands, prefer start_command. 30-minute timeout by default. Use read_command_output or wait_command to monitor a background job started by start_command."
}

func (t RunCommand) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "Shell command to execute. Start with a '# ' comment line describing its purpose (shown as activity label in the UI)."
		},
		"timeout": {
			"type": "integer",
			"description": "Timeout in seconds (default: 1800, max: 86400)",
			"maximum": 86400
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

	if msg := CheckRequired("command", args.Command); msg != "" {
		return Result{IsError: true, Content: "Error: " + msg}, nil
	}

	if args.Description != "" {
		debug.Log("run_command", "description: %s", args.Description)
	}

	// Snapshot tracked file mtimes before execution so we can detect which
	// previously-read files were modified by this command (e.g. gofmt, sed -i,
	// git checkout, protoc). This gives the agent proactive staleness feedback
	// instead of waiting for a stale-read error on the next edit_file.
	mtimeSnapshot := defaultFileTracker.SnapshotTracked()

	gate := NewCommandGate()
	var cleanedCmd, preWarn string
	cleanedCmd, preWarn, blocked := t.applyCommandGate(gate, args.Command)
	if blocked != "" {
		return Result{IsError: true, Content: blocked}, nil
	}
	if cleanedCmd != "" {
		args.Command = cleanedCmd
	}
	preWarning := preWarn

	if args.Timeout <= 0 {
		args.Timeout = int(defaultCommandTimeout / time.Second)
	}
	// #513: clamp before the seconds→nanoseconds multiplication.
	// time.Duration(x)*time.Second has no overflow guard — e.g. 9223372037s
	// wraps negative (WithTimeout expires instantly, command killed at 0s)
	// and 18446744074s wraps to a positive ~290ms. Values above one day
	// are never meaningful for a shell command.
	if args.Timeout > maxCommandTimeoutSeconds {
		args.Timeout = maxCommandTimeoutSeconds
	}

	// #568: GUI commands also return immediately after Start — if their ctx
	// derived from the request context, the deferred cancel would SIGTERM and
	// then SIGKILL the app within ~100ms of the tool call returning, while the
	// success message ("GUI application launched") still told the agent it
	// worked. GUI apps must live on a Background-derived ctx like managed jobs.
	isGUI := isGUICommand(args.Command)
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if t.JobManager != nil || isGUI {
		// Managed background jobs and detached GUI apps outlive this tool
		// call, so their context must not derive from the request context or
		// be deferred here.
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

	// Normalize the terminal environment for all commands so that CLI tools
	// produce clean, deterministic output suitable for LLM consumption:
	//   - TERM=dumb: suppresses terminfo-based color, cursor movement, and
	//     interactive UI (progress bars, spinners) from ncurses/tput-based tools
	//   - NO_COLOR=1: the no-color.org standard honored by Go, Rust, Node,
	//     Python (pytest, rich), and many CLI frameworks; prevents color codes
	//     at the source rather than relying on post-hoc ANSI stripping
	//   - COLUMNS=120: consistent wrapping width so table/list output (kubectl,
	//     terraform, pytest -v) wraps predictably regardless of the user's
	//     actual terminal width
	//   - CI=true: signals non-interactive mode to tools that check for CI
	//     (npm, cargo, gradle, gcloud, etc.), suppressing interactive prompts
	//     and progress bars
	cmd.Env = normalizedCommandEnv()
	if isGitCommand(args.Command) {
		cmd.Env = append(cmd.Env, "GIT_PAGER=cat")
	}
	// Inject Co-Authored-By trailer for git commit commands
	if isGitCommitCommand(args.Command) {
		args.Command = injectCoAuthorTrailer(args.Command)
		newCmd, _, cmdErr := util.NewShellCommandContext(cmdCtx, args.Command)
		if cmdErr != nil {
			return Result{IsError: true, Content: fmt.Sprintf("failed to resolve shell: %v", cmdErr)}, nil
		}
		cmd = newCmd
		configureCommandCancellation(cmd)
		if t.WorkingDir != "" {
			cmd.Dir = t.WorkingDir
		}
		cmd.Env = normalizedCommandEnv()
		if isGitCommand(args.Command) {
			cmd.Env = append(cmd.Env, "GIT_PAGER=cat")
		}
	}

	if t.OnPreExec != nil {
		t.OnPreExec(args.Command, args.Description)
	}

	// Extract progress callback for streaming (if available).
	progressFn, _ := ctx.Value(ToolProgressKey{}).(ToolProgressFunc)

	var stdout, stderr = newBoundedOutputWriter(2 * maxOutputSize), newBoundedOutputWriter(2 * maxOutputSize)
	var pwOut, pwErr *streamingProgressWriter
	pwOut, pwErr = t.wireCommandOutput(cmd, stdout, stderr, progressFn)

	// GUI commands: start and return immediately.
	if isGUI {
		if err := cmd.Start(); err != nil {
			// Release the timeout timer now — nothing else owns cancel on this
			// early-return path.
			cancel()
			return Result{IsError: true, Content: fmt.Sprintf("failed to start GUI command: %v", err)}, nil
		}
		// Detach — don't wait for exit. The guiWait goroutine owns cancel: the
		// timeout clock only fires after the app process has actually exited,
		// so a long-lived GUI app is never killed by the tool call returning
		// (#568).
		safego.Go("tool.runCommand.guiWait", func() {
			_ = cmd.Wait()
			cancel()
		})
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
		return t.executeWithAutoBackground(ctx, cancel, cmd, args.Command, time.Duration(args.Timeout)*time.Second, delay, progressFn)
	}

	err = cmd.Run()

	// Flush any output that landed inside the last 300ms throttle window:
	// Write() only buffers lines while throttled, so a command finishing
	// mid-window would never emit its final tail (the streaming view would
	// silently miss the last burst). The terminal-state SetResult replaces
	// the body afterwards, so this only affects the last streaming frame.
	if progressFn != nil {
		if pwOut != nil {
			pwOut.flush()
		}
		if pwErr != nil {
			pwErr.flush()
		}
	}

	output := util.StripANSI(stdout.String())
	errOutput := util.StripANSI(stderr.String())

	return t.finalizeCommandResult(args.Command, preWarning, output, errOutput, err, mtimeSnapshot), nil
}

// finalizeCommandResult assembles the tool Result after cmd.Run() returns:
// truncation (head+tail), STDERR sectioning, failure diagnostics with exit
// code, mtime-diff file-change notices, and the structured build/test
// summary prefix. Inputs are the already-stripped stdout/stderr strings.
func (t RunCommand) finalizeCommandResult(command, preWarning, output, errOutput string, runErr error, mtimeSnapshot map[string]time.Time) Result {
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

	if runErr != nil {
		exitCode := -1
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		if t.OnPostExec != nil {
			t.OnPostExec(exitCode, runErr)
		}
		return Result{IsError: true, Content: preWarning + t.buildFailureMessage(command, sb.String(), errOutput, runErr, exitCode)}
	}

	if t.OnPostExec != nil {
		t.OnPostExec(0, nil)
	}

	fileChanges := detectChangedFilesFromCommand(mtimeSnapshot)

	if sb.Len() == 0 {
		return Result{Content: preWarning + "Command completed with no output." + fileChanges}
	}

	// Build/test output intelligence: prepend structured summary when available
	// so the agent sees actionable results first, reducing context consumption
	// for large test/build outputs.
	summary := summarizeCommandOutput(command, sb.String())
	if summary != "" {
		return Result{Content: preWarning + summary + sb.String() + fileChanges}
	}

	return Result{Content: preWarning + sb.String() + fileChanges}
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

	// Snap head to rune boundary first, then to end of current line.
	headEnd := util.SnapToRuneStart(s, headSize)
	if idx := strings.Index(s[headEnd:], "\n"); idx >= 0 && idx <= headSize/2 {
		headEnd = headEnd + idx
	}

	// Snap tail to rune boundary first, then to start of next line.
	tailStart := util.SnapToRuneStart(s, len(s)-tailSize)
	if idx := strings.Index(s[tailStart:], "\n"); idx >= 0 && idx <= tailSize/2 {
		tailStart = tailStart + idx + 1
	}

	// If snapping causes overlap, fall back to rune-safe offsets.
	if headEnd >= tailStart {
		headEnd = util.SnapToRuneStart(s, headSize)
		tailStart = util.SnapToRuneStart(s, len(s)-tailSize)
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
func (t RunCommand) executeWithAutoBackground(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, command string, timeout time.Duration, delay time.Duration, progressFn ToolProgressFunc) (Result, error) {
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

	// Stream job output in real-time during the wait period.
	// Poll every 300ms, pushing the last 5 lines to the TUI via progressFn.
	streamJobOutput(job, delay, progressFn)
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

	content := util.StripANSI(commandSnapshotOutput(*snapshot))
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
	// Build/test output intelligence for auto-background completed commands
	if summary := summarizeCommandOutput(command, content); summary != "" {
		return Result{Content: summary + content}, nil
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
		JobManager: t.JobManager,
		Policy:     t.Policy,
		OutputTee:  t.OutputTee,
		OnPreExec:  t.OnPreExec,
		OnPostExec: t.OnPostExec,
	}
}

// diagnoseCommandFailure analyzes command output for common error patterns
// and returns a helpful hint appended to the error message. This gives the
// agent immediate context about likely causes without needing a separate
// diagnostic step.
func diagnoseCommandFailure(stdout, stderr string) string {
	combined := stdout + "\n" + stderr
	var hints []string

	// "command not found" — likely missing dependency or typo
	if strings.Contains(combined, "command not found") || strings.Contains(combined, "not recognized as") {
		hints = append(hints, "Hint: command not found — check if the tool is installed and in PATH")
	}

	// Go compilation errors
	if strings.Contains(combined, "undefined:") && strings.Contains(combined, ".go") {
		hints = append(hints, "Hint: Go undefined reference — check for missing imports or renamed symbols")
	}
	if strings.Contains(combined, "no required module provides package") {
		hints = append(hints, "Hint: missing Go module — run 'go mod tidy' to resolve dependencies")
	}
	if strings.Contains(combined, "cannot find module") || strings.Contains(combined, "cannot find package") {
		hints = append(hints, "Hint: Go package not found — check import path or run 'go mod tidy'")
	}

	// Python import errors
	if strings.Contains(combined, "ModuleNotFoundError") || strings.Contains(combined, "ImportError") {
		hints = append(hints, "Hint: Python module missing — install with pip or check virtual environment")
	}

	// Node/npm errors
	if strings.Contains(combined, "Cannot find module") && strings.Contains(combined, "node") {
		hints = append(hints, "Hint: Node module missing — run 'npm install' or 'yarn install'")
	}
	if strings.Contains(combined, "ELIFECYCLE") {
		hints = append(hints, "Hint: npm script failed — check the script output above for the actual error")
	}

	// Permission denied
	if strings.Contains(combined, "permission denied") {
		hints = append(hints, "Hint: permission denied — check file permissions or ownership")
	}

	// Port already in use
	if strings.Contains(combined, "address already in use") || strings.Contains(combined, "port is already allocated") {
		hints = append(hints, "Hint: port conflict — another process may be using the same port")
	}

	// Make: no rule
	if strings.Contains(combined, "No rule to make target") {
		hints = append(hints, "Hint: Makefile target not found — check spelling or run 'make help' for available targets")
	}

	if len(hints) == 0 {
		return ""
	}
	return "[Diagnostics]\n" + strings.Join(hints, "\n")
}

// streamJobOutput polls a running command job's output and pushes the last
// 5 lines to the TUI via the progress callback. Polls every 300ms for
// near-real-time streaming. Returns when the job finishes or the delay expires.
func streamJobOutput(job *CommandJob, delay time.Duration, progressFn ToolProgressFunc) {
	if progressFn == nil {
		// No progress callback — fall back to simple wait.
		_ = waitForCommandJob(context.Background(), job, delay)
		return
	}

	pollInterval := 300 * time.Millisecond
	deadline := time.Now().Add(delay)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		// Read current output snapshot.
		snap := snapshotJobLines(job, 5)
		if snap != "" {
			progressFn("", "run_command", snap)
		}

		// Check if job is done.
		if isFinishedCommandStatus(jobSnapshotStatus(job)) {
			return
		}

		// Check deadline.
		if !time.Now().Before(deadline) {
			return
		}

		select {
		case <-job.done:
			// Final read after job completes.
			snap := snapshotJobLines(job, 5)
			if snap != "" {
				progressFn("", "run_command", snap)
			}
			return
		case <-ticker.C:
			continue
		}
	}
}

// snapshotJobLines reads the last n lines from a running job's ring buffer.
func snapshotJobLines(job *CommandJob, n int) string {
	job.mu.Lock()
	defer job.mu.Unlock()

	lines := job.Lines
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 0 {
		return ""
	}
	var sb strings.Builder
	statusPrefix := "[...] "
	if isFinishedCommandStatus(job.Status) {
		statusPrefix = fmt.Sprintf("[%s] ", job.Status)
	}
	sb.WriteString(statusPrefix)
	for i, line := range lines {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(util.StripANSI(line))
	}
	return sb.String()
}

// applyCommandGate runs the Allow/Ask/Block gate and interactive-command
// warnings for one command. Returns (cleanedCmd, preExecutionWarning,
// blockedReason): blockedReason non-empty means the caller must return it
// verbatim as an error result; cleanedCmd replaces the command when the
// gate normalized it; preExecutionWarning is prefixed to the final result
// content so the agent can self-correct.
func (t RunCommand) applyCommandGate(gate *CommandGate, command string) (cleanedCmd, preWarning, blocked string) {
	gateResult := gate.Check(command)
	if gateResult.IsBlocked() {
		debug.Log("run_command", "BLOCKED: %s", gateResult.Reason)
		return "", "", gateResult.Reason
	}
	if gateResult.NeedsConfirmation() {
		// In Bypass/Autopilot mode, Ask is automatically downgraded to Allow.
		// These modes assume the user trusts the agent — the command is
		// still logged as a warning for audit purposes.
		if t.isBypassMode() {
			debug.Log("run_command", "ASK→ALLOW (bypass mode): %s", gateResult.Reason)
		} else {
			debug.Log("run_command", "ASK: %s", gateResult.Reason)
			// Caller returns this verbatim — the agent loop interprets the
			// prefix as "needs user permission" and prompts.
			return "", "", "⚠️ " + gateResult.Reason
		}
	}
	for _, w := range gateResult.Warnings {
		debug.Log("run_command", "WARNING: %s", w)
	}
	// Surface interactive-command warnings via the result content so the
	// agent can self-correct before the timeout fires.
	if interactive := gate.InteractiveCommandWarning(command); interactive != "" {
		preWarning = "[Interactive command warning] " + interactive + "\n\n"
	}
	if gateResult.CleanedCmd != "" && gateResult.CleanedCmd != command {
		debug.Log("run_command", "cleaned command: %s → %s", command, gateResult.CleanedCmd)
		cleanedCmd = gateResult.CleanedCmd
	}
	return cleanedCmd, preWarning, ""
}

// buildFailureMessage assembles the error-path result body: combined output,
// optional structured summary, the raw failure, and layered diagnostics
// (root-cause hint, shell-compat hint, exit-code interpretation).
func (t RunCommand) buildFailureMessage(command, combinedOutput, errOutput string, err error, exitCode int) string {
	var msg string
	if summary := summarizeCommandOutput(command, combinedOutput); summary != "" {
		msg = summary + combinedOutput
	} else {
		msg = combinedOutput
	}
	msg += fmt.Sprintf("\nCommand failed: %v", err)
	if diagnostic := diagnoseCommandFailure(combinedOutput, errOutput); diagnostic != "" {
		msg += "\n" + diagnostic
	}
	if compatHint := diagnoseShellCompat(command, combinedOutput, errOutput); compatHint != "" {
		msg += "\n" + compatHint
	}
	if exitIntel := interpretExitCode(exitCode); exitIntel != "" {
		msg += "\n" + exitIntel
	}
	return msg
}

// streamingProgressWriter wraps an io.Writer and pushes the last 5 lines
// of output to the TUI via a progress callback. Used for the direct cmd.Run()
// path (no JobManager) to provide real-time streaming.
type streamingProgressWriter struct {
	buf      io.Writer
	extra    io.Writer
	progress ToolProgressFunc
	// pending accumulates raw chunks since the last emission. ANSI
	// stripping and line splitting are deferred to emit time: they used
	// to run per chunk at PIPE-READ rate, so high-volume commands (builds,
	// installs) burned CPU in the pipe-pump goroutines and the resulting
	// GC pressure stalled the TUI render thread (typing/spinner jank).
	pending  []byte
	lastBody string // most recent emitted tail (flush re-emits)
	lastEmit time.Time
	mu       sync.Mutex
}

// maxPendingProgress bounds the raw chunk backlog between throttled
// emissions. Only the tail is shown, so older bytes are droppable; a
// runaway command must not grow memory unbounded.
const maxPendingProgress = 16 * 1024

func newStreamingProgressWriter(buf io.Writer, extra io.Writer, progress ToolProgressFunc) *streamingProgressWriter {
	return &streamingProgressWriter{
		buf:      buf,
		extra:    extra,
		progress: progress,
	}
}

func (w *streamingProgressWriter) Write(p []byte) (int, error) {
	n := len(p)
	// Write to the underlying buffer first.
	w.buf.Write(p)
	// Write to extra tee if present.
	if w.extra != nil {
		w.extra.Write(p)
	}

	w.mu.Lock()
	w.pending = append(w.pending, p...)
	if len(w.pending) > maxPendingProgress {
		// Keep only the tail.
		w.pending = w.pending[len(w.pending)-maxPendingProgress:]
	}

	// Throttle progress emission to 300ms. The FIRST write emits
	// immediately (lastEmit zero) so output starts scrolling at once;
	// within a window, chunks just accumulate into pending.
	if !w.lastEmit.IsZero() && time.Since(w.lastEmit) < 300*time.Millisecond {
		w.mu.Unlock()
		return n, nil
	}
	w.lastEmit = time.Now()
	body := w.emitTailLocked()
	w.lastBody = body
	w.mu.Unlock()

	w.progress("", "run_command", body)
	return n, nil
}

// emitTailLocked drains pending into the "[...] last-5-lines" body
// (must hold w.mu). Processing happens here, not per chunk.
func (w *streamingProgressWriter) emitTailLocked() string {
	text := util.StripANSI(string(w.pending))
	w.pending = w.pending[:0]
	var tail []string
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		tail = append(tail, line)
		if len(tail) > 5 {
			tail = tail[len(tail)-5:]
		}
	}
	var sb strings.Builder
	sb.WriteString("[...] ")
	for i, line := range tail {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(line)
	}
	return sb.String()
}

// flush forces one final progress emission of the tail, bypassing the
// 300ms throttle. Call after the command exits so the last throttled
// window of output is not lost from the streaming view.
func (w *streamingProgressWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		// Unconditional tail emission (legacy semantics): with nothing
		// new since the last emit, re-emit the last body so callers that
		// flush after command exit always get a final event.
		if w.lastBody != "" {
			w.progress("", "run_command", w.lastBody)
		}
		return
	}
	body := w.emitTailLocked()
	w.lastBody = body
	w.progress("", "run_command", body)
}

// commandEnvOverrides are environment variables injected into every command
// to normalize terminal behavior for clean, deterministic LLM-consumable output.
var commandEnvOverrides = []string{
	"TERM=dumb",   // Suppress color, cursor movement, progress bars
	"NO_COLOR=1",  // no-color.org standard; prevents color at the source
	"COLUMNS=120", // Consistent wrapping width for tables and lists
	"CI=true",     // Non-interactive mode for npm, cargo, gradle, etc.
}

// normalizedCommandEnv returns os.Environ() with terminal normalization
// overrides applied. Later entries take precedence in Go's cmd.Env semantics,
// so the overrides replace any user-set TERM, NO_COLOR, COLUMNS, or CI values.
func normalizedCommandEnv() []string {
	return append(os.Environ(), commandEnvOverrides...)
}

// wireCommandOutput attaches stdout/stderr capture to the cmd with the best
// available pipeline: streaming progress writers (live TUI tail + tee +
// buffer) when a progress callback exists, plain tee+buffer otherwise, or
// buffer-only. Returns the progress writers (nil, nil when progress is off)
// so the caller can flush them after exit.
//
// Buffers are boundedOutputWriter (not bytes.Buffer): command output volume
// is untrusted and raw buffers grew to gigabytes on heavy builds, slowing
// the whole process via memory pressure. Retention cap mirrors the
// post-run truncateMiddle budget (maxOutputSize per stream).
func (t *RunCommand) wireCommandOutput(cmd *exec.Cmd, stdout, stderr *boundedOutputWriter, progressFn ToolProgressFunc) (*streamingProgressWriter, *streamingProgressWriter) {
	if progressFn != nil {
		var tee io.Writer
		if t.OutputTee != nil {
			tee = t.OutputTee
		}
		pwOut := newStreamingProgressWriter(stdout, tee, progressFn)
		pwErr := newStreamingProgressWriter(stderr, tee, progressFn)
		cmd.Stdout = pwOut
		cmd.Stderr = pwErr
		return pwOut, pwErr
	}
	if t.OutputTee != nil {
		cmd.Stdout = io.MultiWriter(stdout, t.OutputTee)
		cmd.Stderr = io.MultiWriter(stderr, t.OutputTee)
		return nil, nil
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return nil, nil
}
