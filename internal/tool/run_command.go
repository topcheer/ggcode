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
	maxOutputSize = 100 * 1024
)

type RunCommand struct {
	WorkingDir string
	JobManager *CommandJobManager
	Policy     permission.PermissionPolicy
	OutputTee  io.Writer
	OnPreExec  func(command, description string)

	OmittedOutputSpiller func(source, omitted string) string
	OnPostExec           func(exitCode int, err error)
	Sandbox              *SandboxPolicy
	SecLedger            *SecurityLedger
	RiskLedger           *AllowedRiskLedger
}

const autoBackgroundDelay = 15 * time.Second

const stalledCommandDelay = 120 * time.Second

var guiCommands = []string{
	"open",
	"xdg-open",
	"start",
	"code",
	"cursor",
	"windsurf",
}

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

func isGUICommand(cmd string) bool {
	firstWord := firstShellWord(cmd)
	for _, gc := range guiCommands {
		if firstWord == gc {
			return true
		}
	}
	return false
}

func isDevServerCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	for _, prefix := range devServerPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func firstShellWord(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}

	if cmd[0] == '"' || cmd[0] == '\'' {
		quote := cmd[0]
		for i := 1; i < len(cmd); i++ {
			if cmd[i] == quote {
				return cmd[1:i]
			}
		}
		return cmd[1:]
	}

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
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		},
		"dry_run": {
			"type": "boolean",
			"description": "Rehearse instead of execute: returns a lexical preview of write targets (create/overwrite/delete) and destructive segments without running anything. Use for high-risk commands before the real call.",
			"default": false
		}
	},
	"required": [
		"command",
		"description"
	]
}`)
}

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
		DryRun      bool   `json:"dry_run"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if msg := CheckRequired("command", args.Command); msg != "" {
		return Result{IsError: true, Content: "Error: " + msg}, nil
	}

	if args.DryRun {
		return t.dryRunPreview(args.Command), nil
	}

	if args.Description != "" {
		debug.Log("run_command", "description: %s", args.Description)
	}

	mtimeSnapshot := defaultFileTracker.SnapshotTracked()

	gate := NewCommandGate()
	var cleanedCmd, preWarn string
	cleanedCmd, preWarn, blocked := t.applyCommandGate(gate, args.Command)
	if blocked != "" {
		t.SecLedger.Record("gate", "block", args.Command)
		if esc := t.SecLedger.Escalation(); esc != "" {
			blocked = blocked + "\n\n" + esc
		}
		return Result{IsError: true, Content: blocked}, nil
	}
	if cleanedCmd != "" {
		args.Command = cleanedCmd
	}
	preWarning := preWarn

	costStart := time.Now()

	t.RiskLedger.Accumulate(args.Command)
	if esc := t.RiskLedger.Escalation(); esc != "" {
		preWarning += esc + "\n\n"
	}

	if args.Timeout <= 0 {
		args.Timeout = int(defaultCommandTimeout / time.Second)
	}

	if args.Timeout > maxCommandTimeoutSeconds {
		args.Timeout = maxCommandTimeoutSeconds
	}

	isGUI := isGUICommand(args.Command)
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if t.JobManager != nil || isGUI {
		if isGUI {

			cmdCtx, cancel = context.WithCancel(context.Background())
		} else {

			cmdCtx, cancel = context.WithTimeout(context.Background(), time.Duration(args.Timeout)*time.Second)
		}
	} else {
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
		defer cancel()
	}

	cmd, _, err := util.NewShellCommandContext(cmdCtx, args.Command)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to resolve shell: %v", err)}, nil
	}
	configureCommandCancellation(cmd)

	if t.WorkingDir != "" {
		cmd.Dir = NearestLiveDir(t.WorkingDir)
	}

	cmd.Env = normalizedCommandEnv()
	if isGitCommand(args.Command) {
		cmd.Env = append(cmd.Env, "GIT_PAGER=cat")
	}

	if isGitCommitCommand(args.Command) {
		args.Command = injectCoAuthorTrailer(args.Command)
		newCmd, _, cmdErr := util.NewShellCommandContext(cmdCtx, args.Command)
		if cmdErr != nil {
			return Result{IsError: true, Content: fmt.Sprintf("failed to resolve shell: %v", cmdErr)}, nil
		}
		cmd = newCmd
		configureCommandCancellation(cmd)
		if t.WorkingDir != "" {
			cmd.Dir = NearestLiveDir(t.WorkingDir)
		}
		cmd.Env = normalizedCommandEnv()
		if isGitCommand(args.Command) {
			cmd.Env = append(cmd.Env, "GIT_PAGER=cat")
		}
	}

	sandboxed := false
	if !isGUI {
		wrapped, wrapErr := wrapShellCommandOS(cmd, t.WorkingDir, t.Sandbox)
		if wrapErr != nil {

			return Result{IsError: true, Content: "Error: " + wrapErr.Error()}, nil
		}
		sandboxed = wrapped
	}

	if t.OnPreExec != nil {
		t.OnPreExec(args.Command, args.Description)
	}

	progressFn, _ := ctx.Value(ToolProgressKey{}).(ToolProgressFunc)

	var stdout, stderr = newBoundedOutputWriter(2 * maxOutputSize), newBoundedOutputWriter(2 * maxOutputSize)
	var pwOut, pwErr *streamingProgressWriter
	pwOut, pwErr = t.wireCommandOutput(cmd, stdout, stderr, progressFn)

	if isGUI {
		if err := cmd.Start(); err != nil {

			cancel()
			return Result{IsError: true, Content: fmt.Sprintf("failed to start GUI command: %v", err)}, nil
		}

		safego.Go("tool.runCommand.guiWait", func() {

			waitErr := cmd.Wait()
			cancel()
			if t.OnPostExec != nil {
				code := 0
				if waitErr != nil {
					code = -1
				}
				t.OnPostExec(code, waitErr)
			}
		})
		return Result{Content: fmt.Sprintf("GUI application launched (pid %d).", cmd.Process.Pid)}, nil
	}

	if t.JobManager != nil {
		delay := stalledCommandDelay
		if isDevServerCommand(args.Command) {
			delay = autoBackgroundDelay
		}
		return t.executeWithAutoBackground(ctx, cancel, cmd, args.Command, time.Duration(args.Timeout)*time.Second, delay, progressFn)
	}

	err = cmd.Run()

	if isSpawnEnoentFailure(err) {
		time.Sleep(shellOutageBackoff)
		if util.ShellAvailable() {
			debug.Log("run_command", "S-16: shell channel self-healed; retrying command after spawn ENOENT")
			if cmd2, pw2o, pw2e, buildErr := t.rebuildShellCommand(cmdCtx, retryCommandFromCmd(cmd, args.Command), cmd, stdout, stderr, progressFn, sandboxed); buildErr == nil {
				cmd, pwOut, pwErr = cmd2, pw2o, pw2e
				err = cmd2.Run()
			}
		}
	}

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

	result := t.finalizeCommandResult(args.Command, preWarning, output, errOutput, err, mtimeSnapshot)

	elapsed := time.Since(costStart)
	recordCommandCost(args.Command, elapsed)
	if costLine := formatCommandCost(args.Command, elapsed); costLine != "" {
		result.Content += "\n\n" + costLine
	}

	if sandboxed && err != nil && sandboxDeniedOutput(output+errOutput) {
		t.SecLedger.Record("sandbox", "eperm", args.Command)
		result.Content += sandboxEPERMHint
		if esc := t.SecLedger.Escalation(); esc != "" {
			result.Content += "\n" + esc
		}
	}

	if isSpawnEnoentFailure(err) {
		result.Content += "\n\n" + shellDegradedHint()
	}
	return result, nil
}

func (t RunCommand) finalizeCommandResult(command, preWarning, output, errOutput string, runErr error, mtimeSnapshot map[string]time.Time) Result {

	output = truncateMiddleSpill(output, maxOutputSize, "output", t.OmittedOutputSpiller)
	errOutput = truncateMiddleSpill(errOutput, maxOutputSize, "stderr", t.OmittedOutputSpiller)

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

		exitCode := exitCodeFromErr(runErr)
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

	summary := summarizeCommandOutput(command, sb.String())
	if summary != "" {
		return Result{Content: preWarning + summary + sb.String() + fileChanges}
	}

	return Result{Content: preWarning + sb.String() + fileChanges}
}

func truncateMiddle(s string, maxLen int, label string) string {
	return truncateMiddleSpill(s, maxLen, label, nil)
}

func truncateMiddleSpill(s string, maxLen int, label string, spiller func(source, omitted string) string) string {
	if len(s) <= maxLen {
		return s
	}
	headSize := maxLen * 2 / 5
	tailSize := maxLen / 2

	headEnd := util.SnapToRuneStart(s, headSize)
	if idx := strings.Index(s[headEnd:], "\n"); idx >= 0 && idx <= headSize/2 {
		headEnd = headEnd + idx
	}

	tailStart := util.SnapToRuneStart(s, len(s)-tailSize)
	if idx := strings.Index(s[tailStart:], "\n"); idx >= 0 && idx <= tailSize/2 {
		tailStart = tailStart + idx + 1
	}

	if headEnd >= tailStart {
		headEnd = util.SnapToRuneStart(s, headSize)
		tailStart = util.SnapToRuneStart(s, len(s)-tailSize)
	}

	head := s[:headEnd]
	tail := s[tailStart:]

	omittedText := s[headEnd:tailStart]
	omittedLines := strings.Count(omittedText, "\n")

	marker := fmt.Sprintf("\n... [%d lines omitted — %s truncated, showing tail] ...\n", omittedLines, label)
	if spiller != nil && len(omittedText) > 0 {
		if notice := spiller(label, omittedText); notice != "" {
			marker += notice
		}
	}

	return head + marker + tail
}

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

		content := snapshot.ErrText
		if isSpawnEnoentFailureText(content) {
			content += "\n\n" + shellDegradedHint()
		}
		return Result{IsError: true, Content: content}, nil
	}
	if job == nil || snapshot == nil {
		cancel()
		return Result{IsError: true, Content: "failed to start command job"}, nil
	}

	streamJobOutput(ctx, job, delay, progressFn)
	snap := t.JobManager.snapshot(job)
	snapshot = &snap
	if snapshot.Status == CommandJobRunning {

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

	return capCommandOutputText(sb.String(), "command output")
}

func (t RunCommand) Clone() Tool {
	return &RunCommand{
		WorkingDir: t.WorkingDir,
		JobManager: t.JobManager,
		Policy:     t.Policy,
		OutputTee:  t.OutputTee,
		OnPreExec:  t.OnPreExec,
		OnPostExec: t.OnPostExec,
		Sandbox:    t.Sandbox,

		SecLedger: t.SecLedger,

		RiskLedger: t.RiskLedger,
	}
}

func retryCommandFromCmd(failed *exec.Cmd, fallback string) string {
	if len(failed.Args) >= 3 {
		return failed.Args[len(failed.Args)-1]
	}
	return fallback
}

const shellOutageBackoff = 2 * time.Second

func isSpawnEnoentFailure(err error) bool {
	if err == nil {
		return false
	}
	return isSpawnEnoentFailureText(err.Error())
}

func isSpawnEnoentFailureText(s string) bool {
	if s == "" {
		return false
	}
	c := strings.ToLower(s)
	fork := strings.Contains(c, "fork/exec")
	if !fork {
		return false
	}
	return strings.Contains(c, "no such file or directory")
}

func shellDegradedHint() string {
	return "[SHELL-DEGRADED] The shell channel failed to spawn (fork/exec ENOENT on the shell itself). " +
		"This is a transient host-level outage observed to self-heal in 1-8 minutes (#4787). " +
		"Do NOT change strategy, rewrite the command, or abandon the task. " +
		"Instead, periodically run the health probe: " + util.ShellHealthProbeCommand + ". " +
		"When it succeeds, re-run the original command unchanged."
}

func (t RunCommand) rebuildShellCommand(ctx context.Context, command string, failed *exec.Cmd, stdout, stderr *boundedOutputWriter, progressFn ToolProgressFunc, sandboxed bool) (*exec.Cmd, *streamingProgressWriter, *streamingProgressWriter, error) {
	cmd2, _, err := util.NewShellCommandContext(ctx, command)
	if err != nil {
		return nil, nil, nil, err
	}
	configureCommandCancellation(cmd2)
	if t.WorkingDir != "" {
		cmd2.Dir = NearestLiveDir(t.WorkingDir)
	}
	cmd2.Env = failed.Env
	if sandboxed {
		if _, wrapErr := wrapShellCommandOS(cmd2, t.WorkingDir, t.Sandbox); wrapErr != nil {
			return nil, nil, nil, wrapErr
		}
	}
	pw2o, pw2e := t.wireCommandOutput(cmd2, stdout, stderr, progressFn)
	return cmd2, pw2o, pw2e, nil
}

func diagnoseCommandFailure(stdout, stderr string) string {
	combined := stdout + "\n" + stderr
	var hints []string

	if strings.Contains(combined, "command not found") || strings.Contains(combined, "not recognized as") {
		hints = append(hints, "Hint: command not found — check if the tool is installed and in PATH")
	}

	if strings.Contains(combined, "undefined:") && strings.Contains(combined, ".go") {
		hints = append(hints, "Hint: Go undefined reference — check for missing imports or renamed symbols")
	}
	if strings.Contains(combined, "no required module provides package") {
		hints = append(hints, "Hint: missing Go module — run 'go mod tidy' to resolve dependencies")
	}
	if strings.Contains(combined, "cannot find module") || strings.Contains(combined, "cannot find package") {
		hints = append(hints, "Hint: Go package not found — check import path or run 'go mod tidy'")
	}

	if strings.Contains(combined, "ModuleNotFoundError") || strings.Contains(combined, "ImportError") {
		hints = append(hints, "Hint: Python module missing — install with pip or check virtual environment")
	}

	if strings.Contains(combined, "Cannot find module") && strings.Contains(combined, "node") {
		hints = append(hints, "Hint: Node module missing — run 'npm install' or 'yarn install'")
	}
	if strings.Contains(combined, "ELIFECYCLE") {
		hints = append(hints, "Hint: npm script failed — check the script output above for the actual error")
	}

	if strings.Contains(combined, "permission denied") {
		hints = append(hints, "Hint: permission denied — check file permissions or ownership")
	}

	if strings.Contains(combined, "address already in use") || strings.Contains(combined, "port is already allocated") {
		hints = append(hints, "Hint: port conflict — another process may be using the same port")
	}

	if strings.Contains(combined, "No rule to make target") {
		hints = append(hints, "Hint: Makefile target not found — check spelling or run 'make help' for available targets")
	}

	if len(hints) == 0 {
		return ""
	}
	return "[Diagnostics]\n" + strings.Join(hints, "\n")
}

func streamJobOutput(ctx context.Context, job *CommandJob, delay time.Duration, progressFn ToolProgressFunc) {
	if progressFn == nil {

		_ = waitForCommandJob(ctx, job, delay)
		return
	}

	pollInterval := 300 * time.Millisecond
	deadline := time.Now().Add(delay)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {

		select {
		case <-ctx.Done():
			return
		default:
		}

		snap := snapshotJobLines(job, 5)
		if snap != "" {
			progressFn("", "run_command", snap)
		}

		if isFinishedCommandStatus(jobSnapshotStatus(job)) {
			return
		}

		if !time.Now().Before(deadline) {
			return
		}

		select {
		case <-ctx.Done():

			return
		case <-job.done:

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

func (t RunCommand) applyCommandGate(gate *CommandGate, command string) (cleanedCmd, preWarning, blocked string) {
	gateResult := gate.Check(command)
	if gateResult.IsBlocked() {
		debug.Log("run_command", "BLOCKED: %s", gateResult.Reason)
		return "", "", gateResult.Reason
	}
	if gateResult.NeedsConfirmation() {

		if t.isBypassMode() {
			debug.Log("run_command", "ASK→ALLOW (bypass mode): %s", gateResult.Reason)

		} else {
			debug.Log("run_command", "ASK: %s", gateResult.Reason)

			return "", "", "⚠️ " + gateResult.Reason
		}
	}
	for _, w := range gateResult.Warnings {
		debug.Log("run_command", "WARNING: %s", w)
	}

	if interactive := gate.InteractiveCommandWarning(command); interactive != "" {
		preWarning = "[Interactive command warning] " + interactive + "\n\n"
	}

	if gateResult.CleanedCmd != "" && gateResult.CleanedCmd != command {
		debug.Log("run_command", "cleaned command: %s → %s", command, gateResult.CleanedCmd)
		cleanedCmd = gateResult.CleanedCmd
	}
	return cleanedCmd, preWarning, ""
}

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

type streamingProgressWriter struct {
	buf      io.Writer
	extra    io.Writer
	progress ToolProgressFunc
	pending  []byte
	lastBody string
	lastEmit time.Time
	mu       sync.Mutex
}

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

	w.buf.Write(p)

	if w.extra != nil {
		w.extra.Write(p)
	}

	w.mu.Lock()
	w.pending = append(w.pending, p...)
	if len(w.pending) > maxPendingProgress {

		w.pending = w.pending[len(w.pending)-maxPendingProgress:]
	}

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

func (w *streamingProgressWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {

		if w.lastBody != "" {
			w.progress("", "run_command", w.lastBody)
		}
		return
	}
	body := w.emitTailLocked()
	w.lastBody = body
	w.progress("", "run_command", body)
}

var commandEnvOverrides = []string{
	"TERM=dumb",
	"NO_COLOR=1",
	"COLUMNS=120",
	"CI=true",
}

func normalizedCommandEnv() []string {

	return append(util.StripSecretEnv(os.Environ()), commandEnvOverrides...)
}

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
