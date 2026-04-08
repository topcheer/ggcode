package harness

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Runner interface {
	Run(context.Context, RunRequest) (*RunResult, error)
}

type RunRequest struct {
	GGCodeConfigPath string
	WorkingDir       string
	Prompt           string
	OnOutput         func(string)
	OnProgress       func(string)
}

type RunResult struct {
	Output   string
	ExitCode int
}

type BinaryRunner struct {
	Executable string
}

type RunSummary struct {
	Task   *Task
	Result *RunResult
}

type RunTaskOptions struct {
	ContextName string
	ContextPath string
}

type RunQueueSummary struct {
	Executed []*RunSummary
}

type QueueRunOptions struct {
	All               bool
	RetryFailed       bool
	ResumeInterrupted bool
	Owner             string
}

func (r BinaryRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	exe := strings.TrimSpace(r.Executable)
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve current executable: %w", err)
		}
	}
	args := []string{"--bypass", "--prompt", req.Prompt}
	if trimmed := strings.TrimSpace(req.GGCodeConfigPath); trimmed != "" {
		args = append([]string{"--config", trimmed}, args...)
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = req.WorkingDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start harness subprocess: %w", err)
	}
	var (
		mu      sync.Mutex
		builder strings.Builder
		wg      sync.WaitGroup
		readErr error
	)
	appendOutput := func(chunk string) {
		mu.Lock()
		builder.WriteString(chunk)
		mu.Unlock()
		if req.OnOutput != nil {
			req.OnOutput(chunk)
		}
	}
	appendProgress := func(line string) {
		mu.Lock()
		builder.WriteString(line)
		builder.WriteByte('\n')
		mu.Unlock()
		if req.OnProgress != nil {
			req.OnProgress(line)
		}
	}
	consumeOutput := func(reader io.Reader) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				appendOutput(string(buf[:n]))
			}
			if err == nil {
				continue
			}
			if err == io.EOF {
				return
			}
			mu.Lock()
			if readErr == nil {
				readErr = err
			}
			mu.Unlock()
			return
		}
	}
	consumeProgress := func(reader io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			appendProgress(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			mu.Lock()
			if readErr == nil {
				readErr = err
			}
			mu.Unlock()
		}
	}
	wg.Add(2)
	go consumeOutput(stdout)
	go consumeProgress(stderr)
	err = cmd.Wait()
	wg.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("read harness subprocess output: %w", readErr)
	}
	result := &RunResult{
		Output: strings.TrimSpace(builder.String()),
	}
	if err == nil {
		return result, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return nil, fmt.Errorf("run harness subprocess: %w", err)
}

func RunTask(ctx context.Context, project Project, cfg *Config, goal string, runner Runner) (*RunSummary, error) {
	return RunTaskWithOptions(ctx, project, cfg, goal, runner, RunTaskOptions{})
}

func RerunTask(ctx context.Context, project Project, cfg *Config, taskID string, runner Runner) (*RunSummary, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("missing task id")
	}
	task, err := LoadTask(project, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != TaskFailed {
		return nil, fmt.Errorf("harness task %s is %s; only failed tasks can be rerun", task.ID, task.Status)
	}
	return ExecuteTask(ctx, project, cfg, task, runner)
}

func RunTaskWithOptions(ctx context.Context, project Project, cfg *Config, goal string, runner Runner, opts RunTaskOptions) (*RunSummary, error) {
	task, err := EnqueueTask(project, goal, "cli", QueueOptions{
		ContextName: opts.ContextName,
		ContextPath: opts.ContextPath,
	})
	if err != nil {
		return nil, err
	}
	return ExecuteTask(ctx, project, cfg, task, runner)
}

func ExecuteTask(ctx context.Context, project Project, cfg *Config, task *Task, runner Runner) (*RunSummary, error) {
	if runner == nil {
		runner = BinaryRunner{}
	}
	if task == nil {
		return nil, fmt.Errorf("missing task")
	}
	if err := refreshTaskStatus(project, task); err != nil {
		return nil, err
	}
	if task.Status == TaskBlocked {
		if err := SaveTask(project, task); err != nil {
			return nil, err
		}
		return &RunSummary{Task: task}, nil
	}
	task.LogPath = filepath.Join(project.LogsDir, task.ID+".log")
	task.Attempt++
	now := time.Now().UTC()
	task.Status = TaskRunning
	task.StartedAt = &now
	task.FinishedAt = nil
	task.Error = ""
	task.WorkerID = ""
	task.WorkerStatus = ""
	task.WorkerPhase = ""
	task.WorkerProgress = ""
	task.ChangedFiles = nil
	task.VerificationStatus = ""
	task.VerificationReportPath = ""
	task.ReviewStatus = ""
	task.ReviewNotes = ""
	task.ReviewedAt = nil
	task.PromotionStatus = ""
	task.PromotionNotes = ""
	task.PromotedAt = nil
	workspace, workspaceErr := PrepareWorkspace(ctx, project, cfg, task)
	if workspace != nil {
		task.WorkspacePath = workspace.Path
		task.WorkspaceMode = workspace.Mode
		task.BranchName = workspace.Branch
	}
	if workspaceErr != nil {
		task.WorkspaceMode = "root-fallback"
		task.Error = workspaceErr.Error()
	}
	if err := SaveTask(project, task); err != nil {
		return nil, err
	}

	workingDir := project.RootDir
	if workspace != nil && strings.TrimSpace(workspace.Path) != "" {
		workingDir = workspace.Path
	}

	prompt := BuildRunPrompt(cfg, task)
	req := RunRequest{
		GGCodeConfigPath: findGGCodeConfig(project.RootDir),
		WorkingDir:       workingDir,
		Prompt:           prompt,
	}
	var (
		result *RunResult
		runErr error
	)
	if shouldUseWorkerExecution(cfg) {
		result, runErr = executeTaskViaWorker(ctx, project, cfg, task, runner, req)
	} else {
		result, runErr = runner.Run(ctx, req)
	}
	if runErr != nil {
		task.Status = TaskFailed
		task.Error = runErr.Error()
		finished := time.Now().UTC()
		task.FinishedAt = &finished
		_ = SaveTask(project, task)
		return nil, runErr
	}
	if task.LogPath != "" && shouldPersistHarnessResultLog(task.LogPath) {
		if err := os.MkdirAll(project.LogsDir, 0755); err == nil {
			_ = os.WriteFile(task.LogPath, []byte(result.Output), 0644)
		}
	}
	deliveryReportPath, deliveryReport, deliveryErr := captureDeliveryReport(ctx, project, cfg, workingDir, task, result.ExitCode == 0)
	if deliveryErr != nil {
		task.Status = TaskFailed
		task.Error = deliveryErr.Error()
		finished := time.Now().UTC()
		task.FinishedAt = &finished
		_ = SaveTask(project, task)
		return nil, deliveryErr
	}
	task.VerificationReportPath = deliveryReportPath
	if deliveryReport != nil {
		task.ChangedFiles = append([]string(nil), deliveryReport.ChangedFiles...)
		if result.ExitCode == 0 {
			if deliveryReport.Check != nil && !deliveryReport.Check.Passed {
				task.VerificationStatus = VerificationFailed
			} else {
				task.VerificationStatus = VerificationPassed
			}
		} else {
			task.VerificationStatus = VerificationSkipped
		}
	}
	finished := time.Now().UTC()
	task.FinishedAt = &finished
	task.ExitCode = result.ExitCode
	if result.ExitCode == 0 {
		if task.VerificationStatus == VerificationFailed {
			task.Status = TaskFailed
			task.Error = "harness verification failed; inspect the delivery report"
		} else {
			task.Status = TaskCompleted
			task.ReviewStatus = ReviewPending
		}
		if task.Status == TaskCompleted && workspaceErr != nil {
			task.Error = workspaceErr.Error()
		} else if task.Status == TaskCompleted {
			task.Error = ""
		}
	} else {
		task.Status = TaskFailed
		task.Error = fmt.Sprintf("ggcode exited with code %d", result.ExitCode)
	}
	if err := SaveTask(project, task); err != nil {
		return nil, err
	}
	return &RunSummary{Task: task, Result: result}, nil
}

func shouldPersistHarnessResultLog(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return info.Size() == 0
}

func RunQueuedTasks(ctx context.Context, project Project, cfg *Config, runner Runner, opts QueueRunOptions) (*RunQueueSummary, error) {
	if runner == nil {
		runner = BinaryRunner{}
	}
	summary := &RunQueueSummary{}
	for {
		if _, err := ListTasks(project); err != nil {
			return nil, err
		}
		task, err := NextRunnableTask(project, cfg, opts)
		if err != nil {
			return nil, err
		}
		if task == nil {
			break
		}
		runSummary, err := ExecuteTask(ctx, project, cfg, task, runner)
		if err != nil {
			return nil, err
		}
		summary.Executed = append(summary.Executed, runSummary)
		if !opts.All {
			break
		}
	}
	return summary, nil
}

func RetryFailedTasksForOwner(ctx context.Context, project Project, cfg *Config, owner string, runner Runner) (*RunQueueSummary, error) {
	return RunQueuedTasks(ctx, project, cfg, runner, QueueRunOptions{
		All:         true,
		RetryFailed: true,
		Owner:       owner,
	})
}

func findGGCodeConfig(root string) string {
	for _, name := range []string{"ggcode.yaml", "ggcode.yml"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func BuildRunPrompt(cfg *Config, task *Task) string {
	if cfg == nil || task == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Harness execution context.\n\n")
	if preamble := strings.TrimSpace(cfg.Run.PromptPreamble); preamble != "" {
		b.WriteString(preamble)
		b.WriteString("\n\n")
	}
	b.WriteString("You are operating inside a repository harness.\n")
	b.WriteString("- Read AGENTS.md and .ggcode/harness.yaml before making changes.\n")
	b.WriteString("- Keep changes aligned with the project goal and deliverables.\n")
	b.WriteString("- Prefer incremental, verifiable changes.\n")
	b.WriteString("- Run the configured harness checks before concluding.\n")
	if len(cfg.Project.Deliverables) > 0 {
		b.WriteString("\nProject deliverables:\n")
		for _, item := range cfg.Project.Deliverables {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}
	if len(cfg.Checks.Commands) > 0 {
		b.WriteString("\nRequired validation commands:\n")
		for _, cmd := range cfg.Checks.Commands {
			b.WriteString("- ")
			b.WriteString(cmd.Run)
			if cmd.Optional {
				b.WriteString(" (optional)")
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nTracked run metadata:\n")
	b.WriteString("- Task ID: " + task.ID + "\n")
	b.WriteString("- Harness goal: " + cfg.Project.Goal + "\n")
	b.WriteString("- Requested work: " + task.Goal + "\n")
	currentAttempt := task.Attempt
	if currentAttempt < 1 {
		currentAttempt = 1
	}
	b.WriteString(fmt.Sprintf("- Attempt: %d/%d\n", currentAttempt, maxTaskAttempts(cfg)))
	if strings.TrimSpace(task.ContextName) != "" || strings.TrimSpace(task.ContextPath) != "" {
		b.WriteString("- Context: " + firstNonEmptyText(task.ContextName, task.ContextPath) + "\n")
		if strings.TrimSpace(task.ContextPath) != "" {
			b.WriteString("- Context AGENTS: " + filepath.Join(task.ContextPath, "AGENTS.md") + "\n")
		}
		if contextCfg := ResolveTaskContext(cfg, task); contextCfg != nil {
			if strings.TrimSpace(contextCfg.Owner) != "" {
				b.WriteString("- Context owner: " + contextCfg.Owner + "\n")
			}
			if len(contextCfg.Commands) > 0 {
				b.WriteString("- Context validation commands:\n")
				for _, cmd := range contextCfg.Commands {
					b.WriteString("  - " + cmd.Run)
					if cmd.Optional {
						b.WriteString(" (optional)")
					}
					b.WriteString("\n")
				}
			}
		}
	}
	if strings.TrimSpace(task.WorkspacePath) != "" {
		b.WriteString("- Workspace: " + task.WorkspacePath + "\n")
	}
	if strings.TrimSpace(task.BranchName) != "" {
		b.WriteString("- Branch: " + task.BranchName + "\n")
	}
	return strings.TrimSpace(b.String())
}

func FormatRunSummary(summary *RunSummary) string {
	if summary == nil || summary.Task == nil {
		return "No harness run executed."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Harness run %s: %s\n", summary.Task.ID, summary.Task.Status))
	if summary.Task.ContextName != "" || summary.Task.ContextPath != "" {
		b.WriteString(fmt.Sprintf("Context: %s\n", firstNonEmptyText(summary.Task.ContextName, summary.Task.ContextPath)))
	}
	if summary.Task.WorkerID != "" {
		b.WriteString(fmt.Sprintf("Worker: %s [%s]\n", summary.Task.WorkerID, summary.Task.WorkerStatus))
	}
	if summary.Task.WorkerProgress != "" {
		b.WriteString(fmt.Sprintf("Progress: %s\n", summary.Task.WorkerProgress))
	}
	if summary.Task.VerificationStatus != "" {
		b.WriteString(fmt.Sprintf("Verification: %s\n", summary.Task.VerificationStatus))
	}
	if len(summary.Task.ChangedFiles) > 0 {
		b.WriteString("Changed files:\n")
		for _, path := range summary.Task.ChangedFiles {
			b.WriteString("  - ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}
	if summary.Task.VerificationReportPath != "" {
		b.WriteString(fmt.Sprintf("Delivery report: %s\n", summary.Task.VerificationReportPath))
	}
	if summary.Task.ReviewStatus != "" {
		b.WriteString(fmt.Sprintf("Review: %s\n", summary.Task.ReviewStatus))
	}
	if summary.Task.PromotionStatus != "" {
		b.WriteString(fmt.Sprintf("Promotion: %s\n", summary.Task.PromotionStatus))
	}
	if summary.Task.LogPath != "" {
		b.WriteString(fmt.Sprintf("Log: %s\n", summary.Task.LogPath))
	}
	if summary.Result != nil && strings.TrimSpace(summary.Result.Output) != "" {
		b.WriteString("\nOutput:\n")
		b.WriteString(indentText(summary.Result.Output, "  "))
	}
	return strings.TrimRight(b.String(), "\n")
}

func FormatQueueSummary(summary *RunQueueSummary) string {
	if summary == nil || len(summary.Executed) == 0 {
		return "No queued harness tasks were executed."
	}
	var b strings.Builder
	b.WriteString("Harness queue run complete.\n")
	for _, item := range summary.Executed {
		if item == nil || item.Task == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s: %s", item.Task.ID, item.Task.Status))
		if item.Task.Goal != "" {
			b.WriteString(" — ")
			b.WriteString(item.Task.Goal)
		}
		if item.Task.Attempt > 0 {
			b.WriteString(fmt.Sprintf(" (attempt %d)", item.Task.Attempt))
		}
		if len(item.Task.DependsOn) > 0 {
			b.WriteString(" (depends on: ")
			b.WriteString(strings.Join(item.Task.DependsOn, ", "))
			b.WriteString(")")
		}
		if strings.TrimSpace(item.Task.Error) != "" {
			b.WriteString(" [")
			b.WriteString(item.Task.Error)
			b.WriteString("]")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
