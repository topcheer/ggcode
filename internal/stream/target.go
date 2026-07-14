package stream

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// TargetState represents the current state of a streaming target.
type TargetState string

const (
	TargetIdle       TargetState = "idle"
	TargetConnecting TargetState = "connecting"
	TargetLive       TargetState = "live"
	TargetError      TargetState = "error"
	TargetStopped    TargetState = "stopped"
)

// TargetStatus holds a snapshot of a target's current state.
type TargetStatus struct {
	Name      string        `json:"name"`
	State     TargetState   `json:"state"`
	URL       string        `json:"url"` // masked URL (key hidden)
	LastError string        `json:"last_error"`
	Uptime    time.Duration `json:"uptime"`
	BytesSent int64         `json:"bytes_sent"`
}

// Target pushes an FLV stream to a single streaming platform via FFmpeg.
type Target struct {
	name string
	url  string // full RTMP/RTMPS URL with stream key

	mu        sync.Mutex
	state     TargetState
	lastError string
	startedAt time.Time
	bytesSent int64

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stopCh chan struct{}

	// broadcastCh receives FLV data from the broadcaster goroutine.
	broadcastCh chan []byte
}

// NewTarget creates a new streaming target.
func NewTarget(name, fullURL string) *Target {
	return &Target{
		name:   name,
		url:    fullURL,
		state:  TargetIdle,
		stopCh: make(chan struct{}),
	}
}

// Name returns the target's display name.
func (t *Target) Name() string { return t.name }

// State returns the current target state (thread-safe).
func (t *Target) State() TargetState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Status returns a snapshot of the target's current status.
func (t *Target) Status() TargetStatus {
	t.mu.Lock()
	defer t.mu.Unlock()

	uptime := time.Duration(0)
	if !t.startedAt.IsZero() {
		uptime = time.Since(t.startedAt)
	}

	// Mask the stream key in URL for display
	displayURL := t.maskURL()

	return TargetStatus{
		Name:      t.name,
		State:     t.state,
		URL:       displayURL,
		LastError: t.lastError,
		Uptime:    uptime,
		BytesSent: t.bytesSent,
	}
}

// Connect starts the FFmpeg subprocess that pushes FLV data to the platform.
// The caller writes FLV data to the returned io.Writer (the target's stdin).
func (t *Target) Connect() (io.Writer, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == TargetLive || t.state == TargetConnecting {
		return nil, fmt.Errorf("target %s already active", t.name)
	}

	t.setState(TargetConnecting)

	// Check ffmpeg availability
	check := CheckFFmpeg()
	if !check.Available {
		t.lastError = check.Error
		t.setState(TargetError)
		return nil, fmt.Errorf("stream: %s", check.Error)
	}
	ffmpegPath := check.Path

	// FFmpeg command: read FLV from stdin, push to RTMP/RTMPS
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-i", "pipe:0",
		"-c", "copy", // no re-encoding, just copy streams
		"-f", "flv",
		t.url,
	}

	t.cmd = exec.Command(ffmpegPath, args...)

	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		t.lastError = err.Error()
		t.setState(TargetError)
		return nil, fmt.Errorf("target %s: stdin pipe: %w", t.name, err)
	}
	t.stdin = stdin

	// Capture target FFmpeg stderr via pipe for diagnostics
	targetStderr, err := t.cmd.StderrPipe()
	if err != nil {
		debug.Log("stream", "target %s: stderr pipe: %v", t.name, err)
	}

	if err := t.cmd.Start(); err != nil {
		t.lastError = err.Error()
		t.setState(TargetError)
		return nil, fmt.Errorf("target %s: start: %w", t.name, err)
	}

	// Monitor target stderr in background
	if targetStderr != nil {
		targetName := t.name
		safego.Go("stream.targetStderr", func() {
			scanner := bufio.NewScanner(targetStderr)
			for scanner.Scan() {
				debug.Log("stream", "target %s stderr: %s", targetName, scanner.Text())
			}
		})
	}

	t.startedAt = time.Now()
	t.bytesSent = 0
	t.lastError = ""
	t.setState(TargetLive)

	return t, nil
}

// Stop terminates the FFmpeg subprocess.
func (t *Target) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == TargetStopped || t.state == TargetIdle {
		return
	}

	t.setState(TargetStopped)

	if t.stdin != nil {
		t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill()
		t.cmd.Wait()
	}
}

// Write forwards FLV data to the target's FFmpeg subprocess.
// Implements io.Writer for fan-out.
func (t *Target) Write(p []byte) (int, error) {
	t.mu.Lock()
	if t.state != TargetLive {
		t.mu.Unlock()
		return 0, fmt.Errorf("target %s not live", t.name)
	}
	writer := t.stdin
	t.mu.Unlock()

	n, err := writer.Write(p)
	if err != nil {
		t.mu.Lock()
		t.lastError = err.Error()
		t.setState(TargetError)
		t.mu.Unlock()
		return n, err
	}

	t.mu.Lock()
	t.bytesSent += int64(n)
	t.mu.Unlock()
	return n, nil
}

func (t *Target) setState(s TargetState) {
	t.state = s
}

// maskURL hides the stream key for display.
func (t *Target) maskURL() string {
	// Find the last / which separates the key
	idx := len(t.url)
	for i := len(t.url) - 1; i >= 0; i-- {
		if t.url[i] == '/' {
			idx = i
			break
		}
	}
	if idx < len(t.url) {
		return t.url[:idx+1] + "***"
	}
	return t.url
}

// maskURLForLog returns a URL with the stream key partially visible for debug logging.
