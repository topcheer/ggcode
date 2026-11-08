package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	defaultCommandTimeout    = 30 * time.Minute
	maxCommandTimeoutSeconds = 86400
	maxCommandLogLines       = 400
	maxCommandLineBytes      = 8 * 1024
	maxCommandSnapshotBytes  = 64 * 1024
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

	// safetyHint is set once at job completion (see waitForJob) and copied
	// into snapshots by CommandJobManager.snapshot.
	safetyHint string
	cancel     context.CancelFunc
	done       chan struct{}
	partial    string
	stdin      io.WriteCloser
	mu         sync.Mutex
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

	// SafetyHint carries a post-completion safety advisory (#4995): a
	// detached-HEAD worktree advisory for successful `git worktree add`
	// jobs, or a push-recovery hint for failed `git push` jobs. Computed
	// once when the job finishes; surfaced by formatCommandJobSnapshot.
	SafetyHint string
}

type CommandJobManager struct {
	workingDir string

	mu            sync.Mutex
	nextID        int
	jobs          map[string]*CommandJob
	finishedOrder []string

	outputTee io.Writer

	sandbox *SandboxPolicy

	maxRunningJobs int
	maxJobMemBytes uint64
}

func NewCommandJobManager(workingDir string) *CommandJobManager {
	return &CommandJobManager{
		workingDir:     workingDir,
		jobs:           make(map[string]*CommandJob),
		maxRunningJobs: jobEnvPositiveInt("GGCODE_MAX_RUNNING_JOBS", maxRunningJobsDefault),
		maxJobMemBytes: resolveJobMemLimit(),
	}
}

const (
	maxRunningJobsDefault = 8
	jobMemLimitDefault    = uint64(3) << 30
)

func jobEnvPositiveInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}

func resolveJobMemLimit() uint64 {
	if n, err := strconv.ParseUint(strings.TrimSpace(os.Getenv("GGCODE_JOB_MEM_LIMIT")), 10, 64); err == nil && n > 0 {
		return n
	}
	if lim := debug.SetMemoryLimit(-1); lim > 0 && lim < 1<<62 {
		return uint64(lim) + uint64(lim)/2
	}
	return jobMemLimitDefault
}

var jobMemSampleFn = func() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Sys
}

func (m *CommandJobManager) admissionBlockReason() string {
	m.mu.Lock()
	running := 0
	for _, j := range m.jobs {
		if !j.isTerminal() {
			running++
		}
	}
	maxRunning := m.maxRunningJobs
	memLimit := m.maxJobMemBytes
	m.mu.Unlock()

	var reasons []string
	if maxRunning > 0 && running >= maxRunning {
		reasons = append(reasons, fmt.Sprintf(
			"concurrent running background jobs at cap (%d/%d, GGCODE_MAX_RUNNING_JOBS) — poll or stop_command an older job before starting another",
			running, maxRunning))
	}
	if memLimit > 0 {
		if sys := jobMemSampleFn(); sys > memLimit {
			reasons = append(reasons, fmt.Sprintf(
				"process memory pressure: %d MiB obtained from OS exceeds the %d MiB ceiling — finish or stop_command running jobs before starting new ones (tune GGCODE_JOB_MEM_LIMIT to override)",
				sys>>20, memLimit>>20))
		}
	}
	return strings.Join(reasons, "; ")
}

func (m *CommandJobManager) ShutdownAll(wait time.Duration) int {
	m.mu.Lock()
	var running []*CommandJob
	for _, j := range m.jobs {
		if !j.isTerminal() {
			running = append(running, j)
		}
	}
	var cancels []context.CancelFunc
	for _, j := range running {
		j.mu.Lock()
		if j.cancel != nil {
			cancels = append(cancels, j.cancel)
		}
		j.mu.Unlock()
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if len(running) == 0 {
		return 0
	}

	deadline := time.Now().Add(wait)
	reaped := 0
	for _, j := range running {
		d := time.Until(deadline)
		if d <= 0 {
			d = time.Millisecond
		}
		select {
		case <-j.done:
			reaped++
		case <-time.After(d):
		}
	}
	return reaped
}

func (m *CommandJobManager) SetOutputTee(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outputTee = w
}

func (m *CommandJobManager) SetSandboxPolicy(p *SandboxPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandbox = p
}

func NearestLiveDir(dir string) string {
	if dir == "" {
		return ""
	}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

func (m *CommandJobManager) Start(ctx context.Context, command string, detach bool, timeout time.Duration) (*CommandJobSnapshot, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	var jobCtx context.Context
	var cancel context.CancelFunc
	if detach {

		jobCtx, cancel = context.WithCancel(context.Background())
		timeout = 0
	} else {
		if timeout <= 0 {
			timeout = defaultCommandTimeout
		}

		jobCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	cmd, _, err := util.NewShellCommandContext(jobCtx, command)
	if err != nil {
		cancel()
		job := m.newJob(command, timeout, cancel)
		job.finish(CommandJobFailed, fmt.Sprintf("failed to resolve shell: %v", err))

		m.mu.Lock()
		m.recordFinishLocked(job)
		m.evictFinishedLocked()
		m.mu.Unlock()
		snapshot := m.snapshot(job)
		return &snapshot, nil
	}
	configureCommandCancellation(cmd)
	if m.workingDir != "" {

		live := NearestLiveDir(m.workingDir)
		cmd.Dir = live
	}

	cmd.Env = normalizedCommandEnv()

	if m.sandbox != nil && m.sandbox.Enabled {
		if _, wrapErr := wrapShellCommandOS(cmd, m.workingDir, m.sandbox); wrapErr != nil {
			cancel()
			job := m.newJob(command, timeout, cancel)
			job.finish(CommandJobFailed, wrapErr.Error())

			m.mu.Lock()
			m.recordFinishLocked(job)
			m.evictFinishedLocked()
			m.mu.Unlock()
			snapshot := m.snapshot(job)
			return &snapshot, nil
		}
	}

	_, snapshot, err := m.startExisting(jobCtx, command, timeout, cancel, cmd)
	if err != nil {

		cancel()
		return nil, err
	}
	return snapshot, nil
}

func (m *CommandJobManager) StartExisting(ctx context.Context, cmd *exec.Cmd, command string, timeout time.Duration, cancel context.CancelFunc) (*CommandJob, *CommandJobSnapshot, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, nil, fmt.Errorf("command is required")
	}
	if cmd == nil {
		return nil, nil, fmt.Errorf("command is required")
	}
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cancel == nil {
		cancel = func() {}
	}
	return m.startExisting(ctx, command, timeout, cancel, cmd)
}

func (m *CommandJobManager) startExisting(ctx context.Context, command string, timeout time.Duration, cancel context.CancelFunc, cmd *exec.Cmd) (*CommandJob, *CommandJobSnapshot, error) {

	if reason := m.admissionBlockReason(); reason != "" {
		return nil, nil, fmt.Errorf("background job refused: %s", reason)
	}
	job := m.newJob(command, timeout, cancel)
	writer := &commandJobWriter{job: job}

	m.mu.Lock()
	tee := m.outputTee
	m.outputTee = nil
	m.mu.Unlock()

	if tee != nil {
		cmd.Stdout = io.MultiWriter(writer, tee)
		cmd.Stderr = io.MultiWriter(writer, tee)
	} else {
		cmd.Stdout = writer
		cmd.Stderr = writer
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		job.finish(CommandJobFailed, fmt.Sprintf("failed to open command stdin: %v", err))
		m.mu.Lock()
		m.recordFinishLocked(job)
		m.evictFinishedLocked()
		m.mu.Unlock()
		snapshot := m.snapshot(job)
		return job, &snapshot, nil
	}
	job.stdin = stdin

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		job.finish(CommandJobFailed, fmt.Sprintf("failed to start command: %v", err))

		m.mu.Lock()
		m.recordFinishLocked(job)
		m.evictFinishedLocked()
		m.mu.Unlock()
		snapshot := m.snapshot(job)
		return job, &snapshot, nil
	}

	safego.Go("tool.commandJob.wait", func() { m.waitForJob(ctx, cmd, job) })
	snapshot := m.snapshot(job)
	return job, &snapshot, nil
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

	snap := m.snapshot(job)
	snap.Lines, snap.TruncatedHead = selectCommandLines(snap.Lines, snap.BufferedFrom, tailLines, sinceLine)
	return snap, nil
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
	const stdinWriteTimeout = 10 * time.Second
	if f, ok := stdin.(*os.File); ok {
		_ = f.SetWriteDeadline(time.Now().Add(stdinWriteTimeout))
		defer func() { _ = f.SetWriteDeadline(time.Time{}) }()
	}
	if _, err := io.WriteString(stdin, input); err != nil {
		if os.IsTimeout(err) {
			return CommandJobSnapshot{}, fmt.Errorf("write command input timed out after %s - the command is not reading stdin (pipe full?)", stdinWriteTimeout)
		}
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
		errMsg := fmt.Sprintf("command failed: %v", err)
		if compatHint := diagnoseShellCompat(job.Command, strings.Join(job.Lines, "\n"), ""); compatHint != "" {
			errMsg += "\n" + compatHint
		}
		if ee, ok := err.(*exec.ExitError); ok {

			if exitIntel := interpretExitCode(exitCodeFromErr(ee)); exitIntel != "" {
				errMsg += "\n" + exitIntel
			}
		}
		// #5278: compute the safety hint BEFORE finish() - the hint write used to
		// happen after close(done), so a Wait()-then-snapshot() caller raced the
		// unlocked write (snapshot reads safetyHint under job.mu) and could read
		// an empty hint. Assigning first keeps the write sequenced before the
		// done signal, inside the goroutine that owns the job fields.
		hint := detectDetachedWorktreePush(context.Background(), job.Command, strings.Join(job.Lines, "\n"), job.ErrText, m.workingDir)
		// #5278: publish the hint under the job lock BEFORE finish() closes done
		// - this gives the locked snapshot reader a happens-before edge through
		// finish's own critical section, so a Wait()-then-snapshot() caller can
		// never race the write or observe an empty hint.
		job.mu.Lock()
		job.safetyHint = hint
		job.mu.Unlock()
		job.finish(CommandJobFailed, errMsg)
	default:
		hint := verifyWorktreeAddResult(context.Background(), job.Command, m.workingDir)
		job.mu.Lock()
		job.safetyHint = hint
		job.mu.Unlock()
		job.finish(CommandJobCompleted, "")
	}

	m.mu.Lock()
	m.recordFinishLocked(job)
	m.evictFinishedLocked()
	m.mu.Unlock()
}

const maxFinishedJobs = 20

func (m *CommandJobManager) recordFinishLocked(job *CommandJob) {
	if _, ok := m.jobs[job.ID]; !ok {
		return
	}
	m.finishedOrder = append(m.finishedOrder, job.ID)
}

func (m *CommandJobManager) evictFinishedLocked() {
	finished := 0
	for _, id := range m.finishedOrder {
		if j, ok := m.jobs[id]; ok && j.isTerminal() {
			finished++
		}
	}
	for _, id := range m.finishedOrder {
		if finished <= maxFinishedJobs {
			break
		}
		j, ok := m.jobs[id]
		if !ok || !j.isTerminal() {
			continue
		}
		delete(m.jobs, id)
		finished--
	}

	kept := m.finishedOrder[:0]
	for _, id := range m.finishedOrder {
		if _, ok := m.jobs[id]; ok {
			kept = append(kept, id)
		}
	}
	m.finishedOrder = kept
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

func (m *CommandJobManager) forget(id string) {
	m.mu.Lock()
	delete(m.jobs, id)
	m.mu.Unlock()
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
		TotalLines:    job.TotalLines,
		BufferedFrom:  job.BufferedFrom,
		Lines:         lines,
		Duration:      endedAt.Sub(job.StartedAt).Round(time.Second),
		ErrText:       job.ErrText,
		Running:       job.Status == CommandJobRunning,
		TruncatedHead: false,
		SafetyHint:    job.safetyHint,
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

		j.partial = truncateJobLine(parts[len(parts)-1])
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

func (j *CommandJob) isTerminal() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Status != CommandJobRunning
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

	if j.cancel != nil {
		j.cancel()
	}
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

	line = truncateJobLine(line)
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

			start = sinceLine - bufferedFrom + 1
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

func truncateJobLine(line string) string {
	if len(line) <= maxCommandLineBytes {
		return line
	}
	headEnd := util.SnapToRuneStart(line, maxCommandLineBytes/2)
	tailStart := util.SnapToRuneStart(line, len(line)-maxCommandLineBytes/4)
	if headEnd >= tailStart {
		tailStart = headEnd
	}
	omitted := len(line) - ((headEnd) + (len(line) - tailStart))
	marker := fmt.Sprintf("\n... [truncated: %d of %d bytes omitted - single-line cap %d bytes] ...\n", omitted, len(line), maxCommandLineBytes)
	return line[:headEnd] + marker + line[tailStart:]
}

func capCommandOutputText(s, label string) string {
	if len(s) > maxCommandSnapshotBytes {
		return truncateMiddle(s, maxCommandSnapshotBytes, label)
	}
	return s
}

// segmentHeaderLike reports whether a line looks like a deliberate
// multi-section separator (the "══/── label ══" style used in compound
// shell commands to fence sections of output). Its purpose here is NOT to
// interpret user style: it detects output that ENDS on such a header with
// no body after it - the signature of a truncated tail (observed #5274:
// compound echo+gh chains occasionally lose mid/late sections to transient
// transport failures, and exit-0 commands look complete when they are not).
func segmentHeaderLike(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	// Runs of 3+ box/light characters (═ ─ ━ ─) possibly with a label.
	n := 0
	for _, r := range t {
		if r == '═' || r == '─' || r == '━' {
			n++
		}
	}
	return n >= 3
}

// detectOrphanSegmentHeaders returns the number of trailing segment
// headers (scanning back over blank lines) that have no body content
// after them.
func detectOrphanSegmentHeaders(lines []string) int {
	i := len(lines) - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	if i < 0 {
		return 0
	}
	count := 0
	for i >= 0 && segmentHeaderLike(lines[i]) {
		count++
		i--
		for i >= 0 && strings.TrimSpace(lines[i]) == "" {
			i--
		}
	}
	return count
}

func formatCommandJobSnapshot(snapshot CommandJobSnapshot, includeLines bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Job ID: %s\n", snapshot.ID))

	if snapshot.Command != "" {
		cmdBlock := snapshot.Command
		if strings.Contains(cmdBlock, "\n") {
			cmdBlock = strings.ReplaceAll(cmdBlock, "\n", "\n    ")
		}
		sb.WriteString(fmt.Sprintf("Command: %s\n", cmdBlock))
	}
	sb.WriteString(fmt.Sprintf("Status: %s\n", snapshot.Status))
	sb.WriteString(fmt.Sprintf("Duration: %s\n", snapshot.Duration))
	if snapshot.Timeout > 0 {
		sb.WriteString(fmt.Sprintf("Timeout: %s\n", snapshot.Timeout.Round(time.Second)))
	} else {
		sb.WriteString("Timeout: none (detached)\n")
	}
	sb.WriteString(fmt.Sprintf("Total lines: %d\n", snapshot.TotalLines))
	if snapshot.TruncatedHead {
		sb.WriteString(fmt.Sprintf("Buffered lines start at: %d\n", snapshot.BufferedFrom))
	}
	if snapshot.ErrText != "" {
		sb.WriteString(fmt.Sprintf("Error: %s\n", util.StripANSI(snapshot.ErrText)))
	}
	if snapshot.SafetyHint != "" {
		sb.WriteString("\n" + snapshot.SafetyHint + "\n")
	}
	if includeLines {
		sb.WriteString("Recent output:\n")
		if len(snapshot.Lines) == 0 {
			sb.WriteString("(no output yet)\n")
		} else {
			for _, line := range snapshot.Lines {
				sb.WriteString(util.StripANSI(line))
				sb.WriteString("\n")
			}
			// #5274: compound commands that fence sections with separator
			// headers should never END on a header - that shape means the
			// last section's body never arrived (transient transport loss).
			// Surface it so the model re-reads instead of trusting a
			// complete-looking tail.
			if orphans := detectOrphanSegmentHeaders(snapshot.Lines); orphans > 0 {
				sb.WriteString(fmt.Sprintf("[warning: output ends on %d segment header(s) with no body - the tail may be truncated; re-read with read_command_output/since_line to verify]\n", orphans))
			}
		}
	}

	return strings.TrimRight(capCommandOutputText(sb.String(), "background job output"), "\n")
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
	status := job.Status
	job.mu.Unlock()

	if status != CommandJobRunning {
		return fmt.Errorf("command job %q already finished (status: %s)", job.ID, status)
	}
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
