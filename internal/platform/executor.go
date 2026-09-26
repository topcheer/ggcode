package platform

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// RunFunc executes one headless agent run. exitCode comes from the process;
// a non-nil err means infrastructure failure (spawn/cancel) rather than the
// agent's own exit status.
type RunFunc func(ctx context.Context, workspace, prompt string) (exitCode int, output []byte, err error)

// DefaultRunFunc runs the local ggcode binary itself in pipe mode, rooted at
// the job's workspace directory - the code stays on the machine that serves
// jobs, which is the platform's core property.
func DefaultRunFunc(selfExe, cfgPath string, bypass bool) RunFunc {
	return func(ctx context.Context, workspace, prompt string) (int, []byte, error) {
		args := []string{"--prompt", prompt}
		if bypass {
			args = append(args, "--bypass")
		}
		if cfgPath != "" {
			args = append(args, "--config", cfgPath)
		}
		cmd := exec.CommandContext(ctx, selfExe, args...)
		cmd.Dir = workspace
		out, err := cmd.CombinedOutput()
		code := 0
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return code, out, err
	}
}

// Executor serializes job execution (one agent run at a time: agent runs are
// memory-heavy and this server shares the box with them) with cancellation
// support for both queued and running jobs.
type Executor struct {
	store *JobStore
	run   RunFunc
	sem   chan struct{}

	mu     sync.Mutex
	cancel map[string]context.CancelFunc
}

// NewExecutor wires an executor onto a store. Pass nil run to use the real
// binary runner via DefaultRunFunc(selfExe, cfgPath, bypass).
func NewExecutor(store *JobStore, run RunFunc) *Executor {
	return &Executor{
		store:  store,
		run:    run,
		sem:    make(chan struct{}, 1),
		cancel: make(map[string]context.CancelFunc),
	}
}

// Submit launches asynchronous execution of a queued job.
func (e *Executor) Submit(job *Job) {
	safego.Go("platform.executor", func() { e.execute(job) })
}

func (e *Executor) execute(job *Job) {
	defer safego.Recover("platform.executor.execute")
	ctx, cf := context.WithCancel(context.Background())
	e.mu.Lock()
	e.cancel[job.ID] = cf
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.cancel, job.ID)
		e.mu.Unlock()
		cf()
	}()

	// Wait for the single execution slot.
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-ctx.Done():
		// Cancelled while waiting for the slot: persist the terminal state
		// so the job does not stay "queued" forever.
		if fresh, err := e.store.Get(job.ID); err == nil && fresh.Status == StatusQueued {
			fresh.Status = StatusCancelled
			fresh.FinishedAt = timeNowUTC()
			_ = e.store.Update(fresh)
		}
		return
	}

	// Re-check state under the store: the job may have been cancelled while
	// waiting for the slot.
	fresh, err := e.store.Get(job.ID)
	if err != nil || fresh.Status != StatusQueued {
		return
	}

	job.Status = StatusRunning
	job.StartedAt = timeNowUTC()
	_ = e.store.Update(job)
	debug.Log("platform", "job %s started in %s", job.ID, job.Workspace)

	code, out, runErr := e.run(ctx, job.Workspace, job.Prompt)
	job.FinishedAt = timeNowUTC()
	job.Output = tailOutput(out)
	switch {
	case ctx.Err() != nil:
		job.Status = StatusCancelled
	case runErr != nil:
		job.Status = StatusFailed
		job.ExitCode = code
		job.Err = runErr.Error()
	default:
		job.Status = StatusSucceeded
		job.ExitCode = code
		if code != 0 {
			job.Status = StatusFailed
			job.Err = fmt.Sprintf("agent exited with code %d", code)
		}
	}
	if err := e.store.Update(job); err != nil {
		debug.Log("platform", "job %s persist failed: %v", job.ID, err)
	}
	debug.Log("platform", "job %s finished: %s", job.ID, job.Status)
}

// Cancel stops a running job (via context cancellation) or flips a queued job
// to cancelled. Returns false when the job is not active.
func (e *Executor) Cancel(id string) bool {
	e.mu.Lock()
	cf, ok := e.cancel[id]
	e.mu.Unlock()
	if ok {
		cf()
		return true
	}
	// Not started yet (or already finished): flip queued jobs directly.
	job, err := e.store.Get(id)
	if err != nil || job.Status != StatusQueued {
		return false
	}
	job.Status = StatusCancelled
	job.FinishedAt = timeNowUTC()
	if err := e.store.Update(job); err != nil {
		debug.Log("platform", "job %s cancel persist failed: %v", id, err)
		return false
	}
	return true
}

// tailOutput keeps the last MaxOutputBytes of combined output.
func tailOutput(out []byte) string {
	if len(out) > MaxOutputBytes {
		out = out[len(out)-MaxOutputBytes:]
		return "...[truncated]\n" + strings.TrimSpace(string(out))
	}
	return strings.TrimSpace(string(out))
}
