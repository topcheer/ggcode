// Package tunnel provides a zero-config anonymous reverse tunnel via localhost.run.
//
// Usage:
//
//	t := tunnel.New(8080)
//	url, err := t.Start()
//	// url = "https://abc123.lhr.life"
//	t.Stop()
package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
)

// Tunnel manages an SSH reverse tunnel to localhost.run.
type Tunnel struct {
	localPort int
	cmd       *exec.Cmd
	url       string
	mu        sync.RWMutex
	done      chan struct{}
}

// Event represents a JSON event from localhost.run --output json.
type Event struct {
	ConnectionID string `json:"connection_id"`
	Event        string `json:"event"`
	Message      string `json:"message"`
	URL          string `json:"url,omitempty"`
}

// New creates a new tunnel for the given local port.
func New(localPort int) *Tunnel {
	return &Tunnel{localPort: localPort}
}

// Start establishes the SSH reverse tunnel and returns the public URL.
// It blocks until the URL is received or an error occurs.
func (t *Tunnel) Start(ctx context.Context) (string, error) {
	// Find ssh binary
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return "", fmt.Errorf("ssh not found in PATH: %w", err)
	}

	localAddr := fmt.Sprintf("127.0.0.1:%d", t.localPort)
	// Verify local port is listening
	conn, err := net.DialTimeout("tcp", localAddr, 2e9)
	if err != nil {
		return "", fmt.Errorf("no service listening on %s: %w", localAddr, err)
	}
	conn.Close()

	// ssh -o StrictHostKeyChecking=no -R 80:127.0.0.1:{port} localhost.run -- --output json
	t.cmd = exec.CommandContext(ctx,
		sshPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "ExitOnForwardFailure=yes",
		"-R", "80:"+localAddr,
		"localhost.run",
		"--", "--output", "json",
	)

	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("pipe: %w", err)
	}
	// Discard stderr (SSH warnings)
	t.cmd.Stderr = io.Discard

	if err := t.cmd.Start(); err != nil {
		return "", fmt.Errorf("ssh start: %w", err)
	}

	t.done = make(chan struct{})

	// Parse JSON output to find the URL
	urlCh := make(chan string, 1)

	go func() {
		defer close(t.done)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var evt Event
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				continue
			}
			if evt.Event == "tcpip-forward" && evt.Message != "" {
				// Message format: "abc123.lhr.life tunneled with tls termination, https://abc123.lhr.life\n..."
				url := extractURL(evt.Message)
				if url != "" {
					t.mu.Lock()
					t.url = url
					t.mu.Unlock()
					select {
					case urlCh <- url:
					default:
					}
				}
			}
		}
	}()

	select {
	case url := <-urlCh:
		return url, nil
	case <-ctx.Done():
		t.Stop()
		return "", ctx.Err()
	case <-t.done:
		return "", fmt.Errorf("ssh process exited before receiving URL")
	}
}

// Stop closes the tunnel.
func (t *Tunnel) Stop() {
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill()
		t.cmd.Wait()
	}
	t.mu.Lock()
	t.url = ""
	t.mu.Unlock()
}

// URL returns the current public URL, or empty string if not connected.
func (t *Tunnel) URL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.url
}

// extractURL extracts the https:// URL from the localhost.run message.
func extractURL(msg string) string {
	for _, part := range strings.Fields(msg) {
		if strings.HasPrefix(part, "https://") {
			// Trim trailing punctuation
			return strings.TrimRight(part, ",;")
		}
	}
	return ""
}
