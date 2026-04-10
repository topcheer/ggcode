package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

const (
	defaultCommandTimeout = 30 * time.Minute
	maxCommandLogLines    = 400
)

type CommandJobStatus string

const (
	CommandJobRunning   CommandJobStatus = "running"
	CommandJobCompleted CommandJobStatus = "completed"
	CommandJobFailed    CommandJobStatus = "failed"
	CommandJobCancelled CommandJobStatus = "cancelled"
	CommandJobTimedOut  CommandJobStatus = "timed_out"
)

type CommandJob struct {
	ID           string
	Command      string
	Status       CommandJobStatus
	Timeout      time.Duration
	StartedAt    time.Time
	EndedAt      time.Time
	TotalLines   int
	BufferedFrom int
	Lines        []string
	ErrText      string

	cancel  context.CancelFunc
	done    chan struct{}
	partial string
	stdin   io.WriteCloser
	mu      sync.Mutex
}

type CommandJobSnapshot struct {
	ID            string
	Command       string
	Status        CommandJobStatus
	Timeout       time.Duration
	StartedAt     time.Time
	EndedAt       time.Time
	TotalLines    int
	BufferedFrom  int
	Lines         []string
	Duration      time.Duration
	ErrText       string
	Running       bool
	TruncatedHead bool
}

type CommandJobManager struct {
	workingDir string

	mu     sync.Mutex
	nextID int
	jobs   map[string]*CommandJob
}

func NewCommandJobManager(workingDir string) *CommandJobManager {
	return &CommandJobManager{
		workingDir: workingDir,
		jobs:       make(map[string]*CommandJob),
	}
}

func (m *CommandJobManager) Start(ctx context.Context, command string, timeout time.Duration) (*CommandJobSnapshot, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	if ctx == nil {
		ctx = context.Background()
	}

	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	cmd, _, err := util.NewShellCommandContext(jobCtx, command)
	if err != nil {
		cancel()
		job := m.newJob(command, timeout, cancel)
		job.finish(CommandJobFailed, fmt.Sprintf("failed to resolve shell: %v", err))
		snapshot := m.snapshot(job)
		return &snapshot, nil
	}
	configureCommandCancellation(cmd)
	if m.workingDir != "" {
		cmd.Dir = m.workingDir
	}

	job := m.newJob(command, timeout, cancel)
	writer := &commandJobWriter{job: job}
	cmd.Stdout = writer
	cmd.Stderr = writer
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		job.finish(CommandJobFailed, fmt.Sprintf("failed to open command stdin: %v", err))
		snapshot := m.snapshot(job)
		return &snapshot, nil
	}
	job.stdin = stdin

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		job.finish(CommandJobFailed, fmt.Sprintf("failed to start command: %v", err))
		snapshot := m.snapshot(job)
		return &snapshot, nil
	}

	go m.waitForJob(jobCtx, cmd, job)
	snapshot := m.snapshot(job)
	return &snapshot, nil
}

func (m *CommandJobManager) List() []CommandJobSnapshot {
	m.mu.Lock()
	jobs := make([]*CommandJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	m.mu.Unlock()

	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].StartedAt.Before(jobs[j].StartedAt)
	})

	out := make([]CommandJobSnapshot, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, m.snapshot(job))
	}
	return out
}

func (m *CommandJobManager) Read(id string, tailLines, sinceLine int) (CommandJobSnapshot, error) {
	job, err := m.get(id)
	if err != nil {
		return CommandJobSnapshot{}, err
	}
	snap := m.snapshot(job)
	snap.Lines, snap.TruncatedHead = selectCommandLines(snap.Lines, snap.BufferedFrom, tailLines, sinceLine)
	return snap, nil
}

func (m *CommandJobManager) Wait(ctx context.Context, id string, wait time.Duration, tailLines, sinceLine int) (CommandJobSnapshot, error) {
	job, err := m.get(id)
	if err != nil {
		return CommandJobSnapshot{}, err
	}
	if err := waitForCommandJob(ctx, job, wait); err != nil {
		return CommandJobSnapshot{}, err
	}

	return m.Read(id, tailLines, sinceLine)
}

func (m *CommandJobManager) Stop(id string) (CommandJobSnapshot, error) {
	job, err := m.get(id)
	if err != nil {
		return CommandJobSnapshot{}, err
	}

	if err := stopCommandJob(job); err != nil {
		return CommandJobSnapshot{}, err
	}

	return m.snapshot(job), nil
}

func (m *CommandJobManager) Write(id, input string, appendNewline bool) (CommandJobSnapshot, error) {
	job, err := m.get(id)
	if err != nil {
		return CommandJobSnapshot{}, err
	}
	job.mu.Lock()
	stdin := job.stdin
	status := job.Status
	job.mu.Unlock()
	if status != CommandJobRunning {
		return CommandJobSnapshot{}, fmt.Errorf("command job %q is not running", id)
	}
	if stdin == nil {
		return CommandJobSnapshot{}, fmt.Errorf("command job %q does not accept input", id)
	}
	if appendNewline {
		input += "\n"
	}
	if _, err := io.WriteString(stdin, input); err != nil {
		return CommandJobSnapshot{}, fmt.Errorf("write command input: %w", err)
	}
	return m.snapshot(job), nil
}

func (m *CommandJobManager) newJob(command string, timeout time.Duration, cancel context.CancelFunc) *CommandJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("cmd-%d", m.nextID)
	job := &CommandJob{
		ID:           id,
		Command:      command,
		Status:       CommandJobRunning,
		Timeout:      timeout,
		StartedAt:    time.Now(),
		cancel:       cancel,
		done:         make(chan struct{}),
		Lines:        make([]string, 0, maxCommandLogLines),
		BufferedFrom: 1,
	}
	m.jobs[id] = job
	return job
}

func (m *CommandJobManager) waitForJob(ctx context.Context, cmd *exec.Cmd, job *CommandJob) {
	err := cmd.Wait()
	job.flushPartial()

	switch {
	case ctx.Err() == context.DeadlineExceeded:
		job.finish(CommandJobTimedOut, fmt.Sprintf("command timed out after %s", job.Timeout.Round(time.Second)))
	case ctx.Err() == context.Canceled:
		job.finish(CommandJobCancelled, "command cancelled")
	case err != nil:
		job.finish(CommandJobFailed, fmt.Sprintf("command failed: %v", err))
	default:
		job.finish(CommandJobCompleted, "")
	}
}

func (m *CommandJobManager) get(id string) (*CommandJob, error) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("command job %q not found", id)
	}
	return job, nil
}

func (m *CommandJobManager) snapshot(job *CommandJob) CommandJobSnapshot {
	job.mu.Lock()
	defer job.mu.Unlock()

	endedAt := job.EndedAt
	if endedAt.IsZero() {
		endedAt = time.Now()
	}

	lines := append([]string(nil), job.Lines...)
	if strings.TrimSpace(job.partial) != "" {
		lines = append(lines, job.partial)
	}

	return CommandJobSnapshot{
		ID:            job.ID,
		Command:       job.Command,
		Status:        job.Status,
		Timeout:       job.Timeout,
		StartedAt:     job.StartedAt,
		EndedAt:       job.EndedAt,
		TotalLines:    job.TotalLines + partialLineCount(job.partial),
		BufferedFrom:  job.BufferedFrom,
		Lines:         lines,
		Duration:      endedAt.Sub(job.StartedAt).Round(time.Second),
		ErrText:       job.ErrText,
		Running:       job.Status == CommandJobRunning,
		TruncatedHead: false,
	}
}

func (j *CommandJob) appendOutput(chunk string) {
	if chunk == "" {
		return
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	chunk = strings.ReplaceAll(chunk, "\r\n", "\n")
	chunk = strings.ReplaceAll(chunk, "\r", "\n")
	combined := j.partial + chunk
	parts := strings.Split(combined, "\n")
	if strings.HasSuffix(combined, "\n") {
		j.partial = ""
		parts = parts[:len(parts)-1]
	} else {
		j.partial = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	for _, line := range parts {
		j.addLineLocked(line)
	}
}

func (j *CommandJob) flushPartial() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.partial == "" {
		return
	}
	j.addLineLocked(j.partial)
	j.partial = ""
}

func (j *CommandJob) finish(status CommandJobStatus, errText string) {
	j.mu.Lock()
	if j.Status != CommandJobRunning {
		j.mu.Unlock()
		return
	}
	j.Status = status
	j.ErrText = errText
	j.EndedAt = time.Now()
	done := j.done
	j.cancel = nil
	stdin := j.stdin
	j.stdin = nil
	j.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	close(done)
}

func (j *CommandJob) addLineLocked(line string) {
	j.TotalLines++
	if len(j.Lines) == maxCommandLogLines {
		j.Lines = j.Lines[1:]
		j.BufferedFrom++
	}
	j.Lines = append(j.Lines, line)
}

type commandJobWriter struct {
	job *CommandJob
}

func (w *commandJobWriter) Write(p []byte) (int, error) {
	if w == nil || w.job == nil {
		return len(p), nil
	}
	w.job.appendOutput(string(p))
	return len(p), nil
}

func partialLineCount(partial string) int {
	if strings.TrimSpace(partial) == "" {
		return 0
	}
	return 1
}

func selectCommandLines(lines []string, bufferedFrom, tailLines, sinceLine int) ([]string, bool) {
	if len(lines) == 0 {
		return nil, false
	}
	if tailLines <= 0 {
		tailLines = 20
	}

	start := 0
	truncated := false
	if sinceLine > 0 {
		if sinceLine >= bufferedFrom {
			start = sinceLine - bufferedFrom
			if start < 0 {
				start = 0
			}
			if start > len(lines) {
				start = len(lines)
			}
		} else {
			truncated = true
		}
	}
	selected := lines[start:]
	if sinceLine <= 0 && len(selected) > tailLines {
		selected = selected[len(selected)-tailLines:]
		truncated = true
	}
	if sinceLine > 0 && len(selected) > tailLines {
		selected = selected[len(selected)-tailLines:]
		truncated = true
	}
	return append([]string(nil), selected...), truncated
}

func formatCommandJobSnapshot(snapshot CommandJobSnapshot, includeLines bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Job ID: %s\n", snapshot.ID))
	sb.WriteString(fmt.Sprintf("Status: %s\n", snapshot.Status))
	sb.WriteString(fmt.Sprintf("Duration: %s\n", snapshot.Duration))
	sb.WriteString(fmt.Sprintf("Timeout: %s\n", snapshot.Timeout.Round(time.Second)))
	sb.WriteString(fmt.Sprintf("Total lines: %d\n", snapshot.TotalLines))
	if snapshot.TruncatedHead {
		sb.WriteString(fmt.Sprintf("Buffered lines start at: %d\n", snapshot.BufferedFrom))
	}
	if snapshot.ErrText != "" {
		sb.WriteString(fmt.Sprintf("Error: %s\n", snapshot.ErrText))
	}
	if includeLines {
		sb.WriteString("Recent output:\n")
		if len(snapshot.Lines) == 0 {
			sb.WriteString("(no output yet)\n")
		} else {
			for _, line := range snapshot.Lines {
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func summarizeCommandProgress(result string) string {
	lines := strings.Split(result, "\n")
	status := ""
	total := ""
	last := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Status: "):
			status = strings.TrimPrefix(line, "Status: ")
		case strings.HasPrefix(line, "Total lines: "):
			total = strings.TrimPrefix(line, "Total lines: ")
		case line != "" && !strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "Job ID:") && !strings.HasPrefix(line, "Duration:") && !strings.HasPrefix(line, "Timeout:") && !strings.HasPrefix(line, "Buffered lines start at:") && !strings.HasPrefix(line, "Error:"):
			last = line
		}
	}
	parts := make([]string, 0, 3)
	if status != "" {
		parts = append(parts, status)
	}
	if total != "" {
		parts = append(parts, total+" lines")
	}
	if last != "" && last != "(no output yet)" {
		parts = append(parts, last)
	}
	return strings.Join(parts, " • ")
}

func isFinishedCommandStatus(status CommandJobStatus) bool {
	switch status {
	case CommandJobCompleted, CommandJobFailed, CommandJobCancelled, CommandJobTimedOut:
		return true
	default:
		return false
	}
}

func waitForCommandJob(ctx context.Context, job *CommandJob, wait time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if isFinishedCommandStatus(jobSnapshotStatus(job)) {
		return nil
	}
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-job.done:
		return nil
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func jobSnapshotStatus(job *CommandJob) CommandJobStatus {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.Status
}

func stopCommandJob(job *CommandJob) error {
	job.mu.Lock()
	cancel := job.cancel
	job.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-job.done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("command is still shutting down")
	}
}
