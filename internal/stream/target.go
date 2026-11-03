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

type TargetState string

const (
	TargetIdle       TargetState = "idle"
	TargetConnecting TargetState = "connecting"
	TargetLive       TargetState = "live"
	TargetError      TargetState = "error"
	TargetStopped    TargetState = "stopped"
)

type TargetStatus struct {
	Name      string        `json:"name"`
	State     TargetState   `json:"state"`
	URL       string        `json:"url"`
	LastError string        `json:"last_error"`
	Uptime    time.Duration `json:"uptime"`
	BytesSent int64         `json:"bytes_sent"`
}

type Target struct {
	name string
	url  string

	mu        sync.Mutex
	state     TargetState
	lastError string
	startedAt time.Time
	bytesSent int64

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stopCh chan struct{}

	broadcastCh chan []byte
}

func NewTarget(name, fullURL string) *Target {
	return &Target{
		name:   name,
		url:    fullURL,
		state:  TargetIdle,
		stopCh: make(chan struct{}),
	}
}

func (t *Target) Name() string { return t.name }

func (t *Target) State() TargetState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *Target) Status() TargetStatus {
	t.mu.Lock()
	defer t.mu.Unlock()

	uptime := time.Duration(0)
	if !t.startedAt.IsZero() {
		uptime = time.Since(t.startedAt)
	}

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

func (t *Target) Connect() (io.Writer, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == TargetLive || t.state == TargetConnecting {
		return nil, fmt.Errorf("target %s already active", t.name)
	}

	t.setState(TargetConnecting)

	check := CheckFFmpeg()
	if !check.Available {
		t.lastError = check.Error
		t.setState(TargetError)
		return nil, fmt.Errorf("stream: %s", check.Error)
	}
	ffmpegPath := check.Path

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-i", "pipe:0",
		"-c", "copy",
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

	targetStderr, err := t.cmd.StderrPipe()
	if err != nil {
		debug.Log("stream", "target %s: stderr pipe: %v", t.name, err)
	}

	if err := t.cmd.Start(); err != nil {

		_ = stdin.Close()
		t.stdin = nil
		t.lastError = err.Error()
		t.setState(TargetError)
		return nil, fmt.Errorf("target %s: start: %w", t.name, err)
	}

	stderrDone := make(chan struct{})
	if targetStderr != nil {
		targetName := t.name
		safego.Go("stream.targetStderr", func() {
			defer close(stderrDone)
			scanner := bufio.NewScanner(targetStderr)
			scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
			for scanner.Scan() {
				debug.Log("stream", "target %s stderr: %s", targetName, scanner.Text())
			}
			if err := scanner.Err(); err != nil {
				debug.Log("stream", "target %s stderr read: %v", targetName, err)
			}
		})
	} else {
		close(stderrDone)
	}

	monCmd := t.cmd
	monName := t.name
	safego.Go("stream.targetMonitor", func() {
		<-stderrDone
		werr := monCmd.Wait()
		t.mu.Lock()

		if t.state == TargetLive && t.cmd == monCmd {
			if werr != nil {
				t.lastError = fmt.Sprintf("ffmpeg exited: %v", werr)
			} else {
				t.lastError = "ffmpeg exited"
			}
			t.setState(TargetError)
		}
		t.mu.Unlock()
		debug.Log("stream", "target %s ffmpeg exited: %v", monName, werr)
	})

	t.startedAt = time.Now()
	t.bytesSent = 0
	t.lastError = ""
	t.setState(TargetLive)

	return t, nil
}

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
		_ = t.cmd.Process.Kill()
	}
}

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

func (t *Target) maskURL() string {

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
