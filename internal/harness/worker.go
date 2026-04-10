package harness

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

const defaultWorkerTimeout = 30 * time.Minute

type workerRunResult struct {
	result *RunResult
	err    error
}

func shouldUseWorkerExecution(cfg *Config) bool {
	if cfg == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Run.ExecutionMode)) {
	case "", "subagent", "worker":
		return true
	case "direct":
		return false
	default:
		return true
	}
}

func executeTaskViaWorker(ctx context.Context, project Project, cfg *Config, task *Task, runner Runner, req RunRequest) (*RunResult, error) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 1,
		Timeout:       defaultWorkerTimeout,
	})
	workerTask := task.Goal
	if strings.TrimSpace(workerTask) == "" {
		workerTask = task.ID
	}
	workerID := mgr.Spawn(workerTask, workerTask, []string{"harness-worker"}, ctx)
	task.WorkerID = workerID
	task.WorkerStatus = string(subagent.StatusPending)
	task.WorkerPhase = "pending"
	task.WorkerProgress = "queued harness worker"
	if err := SaveTask(project, task); err != nil {
		return nil, err
	}

	workerCtx, cancel := context.WithCancel(ctx)
	mgr.SetCancel(workerID, cancel)
	mgr.UpdateActivity(workerID, "tool", "harness-worker", req.WorkingDir)
	mgr.UpdateProgress(workerID, fmt.Sprintf("launching harness worker in %s", req.WorkingDir))
	var (
		logMu   sync.Mutex
		logFile *os.File
	)
	if strings.TrimSpace(task.LogPath) != "" {
		if err := os.MkdirAll(project.LogsDir, 0755); err == nil {
			if f, err := os.Create(task.LogPath); err == nil {
				logFile = f
				defer logFile.Close()
			}
		}
	}
	req.OnOutput = func(chunk string) {
		if logFile != nil {
			logMu.Lock()
			_, _ = logFile.WriteString(chunk)
			logMu.Unlock()
		}
	}
	req.OnProgress = func(line string) {
		line = strings.TrimRight(line, "\r\n")
		if logFile != nil && line != "" {
			logMu.Lock()
			_, _ = logFile.WriteString(line + "\n")
			logMu.Unlock()
		}
		if summary := summarizeWorkerOutputLine(line); summary != "" {
			mgr.UpdateProgress(workerID, summary)
		}
	}

	done := make(chan workerRunResult, 1)
	go func() {
		result, err := runner.Run(workerCtx, req)
		if err != nil {
			mgr.UpdateProgress(workerID, fmt.Sprintf("worker failed: %v", err))
			mgr.Complete(workerID, "", err)
			done <- workerRunResult{err: err}
			return
		}
		mgr.UpdateProgress(workerID, summarizeWorkerResult(result))
		mgr.Complete(workerID, result.Output, nil)
		done <- workerRunResult{result: result}
	}()

	for {
		snap, err := subagent.WaitForSnapshot(ctx, mgr, workerID, 200*time.Millisecond)
		if err != nil {
			cancel()
			return nil, err
		}
		task.WorkerStatus = string(snap.Status)
		task.WorkerPhase = snap.CurrentPhase
		task.WorkerProgress = snap.ProgressSummary
		if err := SaveTask(project, task); err != nil {
			cancel()
			return nil, err
		}
		switch snap.Status {
		case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled:
			cancel()
			outcome := <-done
			return outcome.result, outcome.err
		}
	}
}

func summarizeWorkerResult(result *RunResult) string {
	if result == nil {
		return "worker finished"
	}
	if result.ExitCode != 0 {
		if detail := summarizeHarnessRunFailure(result.Output); detail != "" {
			return truncateWorkerText(fmt.Sprintf("worker failed with exit code %d: %s", result.ExitCode, detail), 120)
		}
		return fmt.Sprintf("worker failed with exit code %d", result.ExitCode)
	}
	lines := strings.Split(strings.TrimSpace(result.Output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			return truncateWorkerText(line, 120)
		}
	}
	return "worker completed"
}

func summarizeWorkerOutputLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	return truncateWorkerText(line, 120)
}

func truncateWorkerText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	if maxLen < 4 {
		return text[:maxLen]
	}
	return text[:maxLen-3] + "..."
}
